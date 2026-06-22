package disk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
)

// dfArgs fetches the verbose disk dump from the daemon the docker CLI is pointed at.
var dfArgs = []string{"system", "df", "-v", "--format", "json"}

// ScanDocker queries reachable Docker daemons and returns the DOCKER domain, or
// nil if none reachable. The default socket is the "host" daemon. A "dind"
// CI-build daemon is reached either via DOCKER_HOST or by auto-discovering a
// running docker:dind container and querying it through `docker exec`.
func ScanDocker(ctx context.Context) *Node {
	dom := &Node{Label: "DOCKER"}
	if d := queryDaemon(ctx, nil, dfArgs, "docker host daemon", dockerEndpoint(), "docker-host", "dev images"); d != nil {
		dom.add(d)
	}
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		if d := queryDaemon(ctx, []string{"DOCKER_HOST=" + h}, dfArgs, "docker dind daemon", h, "docker-dind", "build-base images"); d != nil {
			dom.add(d)
		}
	}
	for _, c := range discoverDind(ctx) {
		args := append([]string{"exec", c, "docker"}, dfArgs...)
		if d := queryDaemon(ctx, nil, args, "docker dind daemon", c, "docker-dind", "build-base images"); d != nil {
			dom.add(d)
		}
	}
	if len(dom.Kids) == 0 {
		return nil
	}
	for _, c := range dom.Kids {
		dom.Bytes += c.Bytes
	}
	return dom // daemons in detection order (host, dind…)
}

// discoverDind returns the names of running containers whose image is a docker
// dind image — the live CI build daemons reachable via `docker exec`.
func discoverDind(ctx context.Context) []string {
	lines, err := dockerJSON(ctx, nil, "ps", "--format", "{{.Image}}\t{{.Names}}")
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range lines {
		parts := strings.SplitN(string(l), "\t", 2)
		if len(parts) == 2 && strings.Contains(parts[0], "dind") {
			out = append(out, strings.TrimSpace(parts[1]))
		}
	}
	return out
}

func dockerEndpoint() string {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h
	}
	return "/var/run/docker.sock"
}

// dfDump is the shape of `docker system df -v --format json`.
type dfDump struct {
	Images []struct {
		Repository, Tag, ID, CreatedSince, Size, SharedSize, UniqueSize string
	}
	Volumes []struct {
		Name, Links, Size string
	}
	Containers []struct {
		Names, Status, Size string
	}
	BuildCache []struct {
		Size string
	}
}

func queryDaemon(ctx context.Context, env, args []string, label, endpoint, runtime, otherLabel string) *Node {
	lines, err := dockerJSON(ctx, env, args...)
	if err != nil || len(lines) == 0 {
		return nil // unreachable
	}
	var df dfDump
	if json.Unmarshal(lines[0], &df) != nil {
		return nil
	}
	d := &Node{Label: label, Note: endpoint, Attr: Attribution{Runtime: runtime}}

	d.addNonZero(scanDockerImages(df, runtime, otherLabel)...)
	if v := scanDockerVolumes(df, runtime); v != nil {
		d.add(v)
	}
	if bc := sumSizes(buildCacheSizes(df)); bc > 0 {
		d.add(&Node{Label: "build cache", Bytes: bc, Flags: FlagReclaimable,
			Hint: &Hint{Command: "docker buildx prune", Safety: "safe"}, Attr: Attribution{Runtime: runtime}})
	}
	if cn, cb := stoppedContainers(df); cb > 0 {
		d.add(&Node{Label: "stopped containers", Bytes: cb, Note: fmt.Sprintf("%d exited", cn),
			Flags: FlagReclaimable, Hint: &Hint{Command: "docker container prune", Safety: "safe"},
			Attr: Attribution{Runtime: runtime}})
	}

	d.sortKids()
	for _, c := range d.Kids {
		d.Bytes += c.Bytes
	}
	return d
}

func (n *Node) addNonZero(kids ...*Node) {
	for _, k := range kids {
		if k != nil && k.Bytes > 0 {
			n.add(k)
		}
	}
}

// ── images (deduplicated by UniqueSize, grouped into families) ──────────────

func scanDockerImages(df dfDump, runtime, otherLabel string) []*Node {
	families := map[string]*Node{}
	order := []string{}
	fam := func(key, lbl string) *Node {
		n := families[key]
		if n == nil {
			n = &Node{Label: lbl, Attr: Attribution{Runtime: runtime}}
			families[key] = n
			order = append(order, key)
		}
		return n
	}
	seenID := map[string]bool{} // dedup multi-tag images by ID for size
	for _, im := range df.Images {
		if im.Repository == "" {
			continue
		}
		sz := parseDockerSize(im.UniqueSize)
		if im.Repository == "<none>" {
			dg := fam("dangling", "dangling")
			if !seenID[im.ID] {
				seenID[im.ID] = true
				dg.add(&Node{Label: shortID(im.ID), Bytes: sz, Note: im.CreatedSince,
					Attr: Attribution{Runtime: runtime}})
			}
			continue
		}
		key, lbl := imageFamily(im.Repository)
		if lbl == "" {
			lbl = otherLabel
		}
		f := fam(key, lbl)
		repo := childByLabel(f, im.Repository)
		if repo == nil {
			repo = &Node{Label: im.Repository, Attr: Attribution{
				Runtime: runtime, Registry: registryOf(im.Repository), Project: projectOf(im.Repository)}}
			f.add(repo)
		}
		if !seenID[im.ID] {
			repo.Bytes += sz
			seenID[im.ID] = true
		}
		if im.Tag != "" && im.Tag != "<none>" {
			repo.Note = appendTag(repo.Note, im.Tag)
		}
	}

	var out []*Node
	for _, key := range order {
		f := families[key]
		dangling := key == "dangling"
		for _, repo := range f.Kids {
			f.Bytes += repo.Bytes
			if !dangling {
				repo.Note = bracket(repo.Note)
			}
		}
		switch key {
		case "dangling":
			f.Flags = FlagReclaimable
			f.Hint = &Hint{Command: "docker image prune", Safety: "safe"}
			f.Note = "untagged layers"
		case "stagefreight":
			if regs, tags := registrySpread(f); regs > 1 || tags > len(f.Kids) {
				f.Flags = FlagAttention
				f.Note = fmt.Sprintf("%d registries · %d tags", regs, tags)
			}
		}
		f.sortKids()
		out = append(out, f)
	}
	return out
}

// ── volumes (per-volume, attributed, unused→reclaimable) ────────────────────

func scanDockerVolumes(df dfDump, runtime string) *Node {
	v := &Node{Label: "volumes", Attr: Attribution{Runtime: runtime}}
	for _, vol := range df.Volumes {
		sz := parseDockerSize(vol.Size)
		n := &Node{Label: volumeLabel(vol.Name), Bytes: sz, Note: volumeNote(vol.Name),
			Attr: volumeAttr(vol.Name, runtime)}
		if vol.Links == "0" || vol.Links == "" || vol.Links == "N/A" {
			n.Flags |= FlagReclaimable // unused
		}
		if isAnonymousVol(vol.Name) {
			n.Flags |= FlagReclaimable
		}
		if n.Flags.Has(FlagReclaimable) {
			n.Hint = &Hint{Command: "docker volume rm " + vol.Name, Safety: "safe"}
		}
		v.add(n)
		v.Bytes += sz
	}
	v.sortKids()
	if v.Bytes == 0 && len(v.Kids) == 0 {
		return nil
	}
	if v.Bytes > 0 {
		v.Hint = &Hint{Command: "docker volume prune", Safety: "inspect first"}
		v.Flags |= FlagAttention
	}
	return v
}

func isAnonymousVol(name string) bool {
	if len(name) != 64 {
		return false
	}
	for _, r := range name {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func volumeLabel(name string) string {
	if isAnonymousVol(name) {
		return "anonymous " + name[:12]
	}
	return name
}

func volumeNote(name string) string {
	switch {
	case isAnonymousVol(name):
		return "orphaned"
	case strings.Contains(name, "dind-storage"):
		return "dind backing store"
	case strings.HasPrefix(name, "sf-"):
		return "stagefreight build cache"
	default:
		return ""
	}
}

// volumeAttr ties stagefreight build-cache volumes back to their project/tool, so
// e.g. sf-dragonfly-target rolls into dragonfly's by-project footprint.
func volumeAttr(name, runtime string) Attribution {
	a := Attribution{Runtime: runtime}
	switch {
	case strings.HasPrefix(name, "sf-") && strings.HasSuffix(name, "-target"):
		a.Project = strings.TrimSuffix(strings.TrimPrefix(name, "sf-"), "-target")
		a.Tool = "rust"
	case strings.HasPrefix(name, "sf-gocache"):
		a.Tool = "go"
	case strings.HasPrefix(name, "sf-cargo"):
		a.Tool = "cargo"
	}
	return a
}

// ── helpers ─────────────────────────────────────────────────────────────────

func buildCacheSizes(df dfDump) []string {
	out := make([]string, len(df.BuildCache))
	for i, c := range df.BuildCache {
		out[i] = c.Size
	}
	return out
}

func sumSizes(sizes []string) int64 {
	var t int64
	for _, s := range sizes {
		t += parseDockerSize(s)
	}
	return t
}

func stoppedContainers(df dfDump) (count int, bytes int64) {
	for _, c := range df.Containers {
		if !strings.HasPrefix(c.Status, "Up") {
			count++
			bytes += parseDockerSize(c.Size)
		}
	}
	return
}

func imageFamily(repo string) (key, label string) {
	base := path.Base(repo)
	switch {
	case base == "stagefreight" || strings.HasSuffix(repo, "/stagefreight"):
		return "stagefreight", "stagefreight images"
	case repo == "docker" || strings.Contains(repo, "gitlab-runner") ||
		strings.Contains(repo, "buildkit") || strings.Contains(repo, "auto-build-image"):
		return "ci-infra", "CI-infra images"
	default:
		return "other", ""
	}
}

// baseImages are tools/infrastructure, not developed projects — they never
// attribute to a project, so by-project surfaces your apps, not base images.
var baseImages = map[string]bool{
	"rust": true, "golang": true, "go": true, "node": true, "alpine": true,
	"ubuntu": true, "debian": true, "busybox": true, "python": true,
	"code-server": true, "docker": true, "buildkit": true, "dind": true,
	"gitlab-runner": true, "gitlab-runner-helper": true, "auto-build-image": true,
	"portainer": true, "agent": true, "registry": true, "nginx": true,
}

// projectOf attributes an image to a project by its name (basename), unless it is
// a base/infra image. This lets an image named "hasteward" roll into the
// hasteward project alongside its repo.
func projectOf(repo string) string {
	base := path.Base(repo)
	if base == "" || baseImages[base] {
		return ""
	}
	return base
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	return "untagged " + id
}

func registryOf(repo string) string {
	if i := strings.IndexByte(repo, '/'); i > 0 {
		if host := repo[:i]; strings.ContainsAny(host, ".:") || host == "localhost" {
			return host
		}
	}
	return "docker.io"
}

func registrySpread(fam *Node) (registries, tags int) {
	regs := map[string]bool{}
	for _, r := range fam.Kids {
		regs[r.Attr.Registry] = true
		tags += strings.Count(r.Note, "·") + 1
	}
	return len(regs), tags
}

func childByLabel(n *Node, label string) *Node {
	for _, c := range n.Kids {
		if c.Label == label {
			return c
		}
	}
	return nil
}

func appendTag(note, tag string) string {
	if note == "" {
		return tag
	}
	return note + " · " + tag
}

func bracket(note string) string {
	if note == "" {
		return ""
	}
	return "[" + note + "]"
}

func dockerJSON(ctx context.Context, env []string, args ...string) ([][]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var lines [][]byte
	for _, l := range bytes.Split(out, []byte("\n")) {
		if len(bytes.TrimSpace(l)) > 0 {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

func parseDockerSize(s string) int64 {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '('); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if s == "" || s == "N/A" {
		return 0
	}
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	num, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0
	}
	mult := 1.0
	switch strings.ToLower(strings.TrimSpace(s[i:])) {
	case "", "b":
		mult = 1
	case "kb":
		mult = 1e3
	case "mb":
		mult = 1e6
	case "gb":
		mult = 1e9
	case "tb":
		mult = 1e12
	case "kib":
		mult = 1 << 10
	case "mib":
		mult = 1 << 20
	case "gib":
		mult = 1 << 30
	case "tib":
		mult = 1 << 40
	}
	return int64(num * mult)
}
