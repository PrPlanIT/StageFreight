package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// RepoFileReader reads a repo-relative file. A missing file is fs.ErrNotExist.
type RepoFileReader func(rel string) ([]byte, error)

// UpstreamAnnotation is the file position of a renovate annotation's registryUrl release
// (apk), which moves with the Dockerfile's Alpine FROM.
type UpstreamAnnotation struct {
	File   string
	Line   int
	Column int
	Value  string
}

const renovateMarker = "renovate:"

// renovateVersioning maps Renovate versioning names onto version schemes.
var renovateVersioning = map[string]string{
	"semver": "semver", "semver-coerced": "semver", "pep440": "pep440", "loose": "loose",
	"deb": "debian", "debian": "debian",
}

var annotatedPin = regexp.MustCompile(`^\s*(?i:ARG|ENV)\s+([A-Za-z_][A-Za-z0-9_]*)[=\s]`)

// continuedPin is a key=value continuation line of an ARG or ENV.
var continuedPin = regexp.MustCompile(`^\s+([A-Za-z_][A-Za-z0-9_]*)=`)

var argOrEnv = regexp.MustCompile(`^\s*(?i:ARG|ENV)\s`)

var alpineRegistry = regexp.MustCompile(`^https?://[^/]+/alpine/(v\d+\.\d+)/(main|community)/?$`)

// annotationFiles are the Dockerfiles of the docker builds, each once.
func annotationFiles(cfg *Config) []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range cfg.Builds {
		if b.Kind != "docker" {
			continue
		}
		f := b.Dockerfile
		if f == "" {
			f = path.Join(path.Clean(strings.TrimPrefix(b.Context, "./")), "Dockerfile")
		}
		f = path.Clean(f)
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// applyUpstreamAnnotations declares a source, with its pin, for every renovate annotation
// above an ARG or ENV, or above a key=value continuation line of one, in a docker build's
// Dockerfile. A source the config declares under the same id keeps every field it sets;
// the annotation adds its pin.
func applyUpstreamAnnotations(cfg *Config, read RepoFileReader) error {
	if read == nil {
		return nil
	}
	var errs []string
	for _, file := range annotationFiles(cfg) {
		data, err := read(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading %s for renovate annotations: %w", file, err)
		}
		lines := strings.Split(string(data), "\n")
		inside := false // on a continuation line of an ARG or ENV
		for i, l := range lines {
			comment := strings.HasPrefix(strings.TrimSpace(l), "#")
			wasInside := inside
			switch {
			case argOrEnv.MatchString(l):
				inside = strings.HasSuffix(strings.TrimRight(l, " \t"), "\\")
			case inside && !comment:
				inside = strings.HasSuffix(strings.TrimRight(l, " \t"), "\\")
			}
			at := strings.Index(l, renovateMarker)
			if at < 0 || !comment {
				continue
			}
			where := fmt.Sprintf("%s:%d", file, i+1)
			next := ""
			if i+1 < len(lines) {
				next = lines[i+1]
			}
			m := annotatedPin.FindStringSubmatch(next)
			if m == nil && wasInside {
				m = continuedPin.FindStringSubmatch(next)
			}
			if m == nil {
				continue // an image reference (dependency.scope.source_images), not a version pin
			}
			src, err := parseAnnotation(l[at+len(renovateMarker):], where)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			if src.Apk != "" && src.Suite != "" {
				col := strings.Index(l, "/"+strings.SplitN(src.Suite, "/", 2)[0]+"/") + 1
				src.registry = &UpstreamAnnotation{File: file, Line: i + 1, Column: col, Value: strings.SplitN(src.Suite, "/", 2)[0]}
				src.Repository, src.Suite = strings.SplitN(src.Suite, "/", 2)[1], ""
			}
			src.Pins = []UpstreamPin{{File: file, Arg: m[1]}}
			src.Annotation = where
			if err := mergeAnnotated(cfg, src); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}
	return nil
}

// parseAnnotation reads datasource, depName, registryUrl, extractVersion and versioning.
func parseAnnotation(body, where string) (UpstreamSource, error) {
	f := map[string]string{}
	for _, tok := range strings.Fields(body) {
		if k, v, ok := strings.Cut(tok, "="); ok {
			f[k] = v
		}
	}
	dep := f["depName"]
	if pn := f["packageName"]; pn != "" {
		dep = pn
	}
	if dep == "" || strings.Contains(dep, "$") {
		return UpstreamSource{}, fmt.Errorf("%s: renovate annotation needs a literal depName", where)
	}
	src := UpstreamSource{ID: strings.ToLower(path.Base(strings.TrimSuffix(dep, "/")))}
	reg := f["registryUrl"]
	switch ds := f["datasource"]; ds {
	case "pypi":
		src.PyPI = dep
	case "npm":
		src.Npm = dep
	case "docker":
		src.Image = dep
	case "github-releases":
		src.GitHubRelease = dep
	case "github-tags":
		src.Git = "https://github.com/" + dep
	case "gitlab-tags":
		if reg == "" {
			reg = "https://gitlab.com"
		}
		src.Git = strings.TrimSuffix(reg, "/") + "/" + dep
	case "git-tags":
		src.Git = dep
	case "apk":
		src.Apk = dep
		src.Repository = "main"
		if reg != "" {
			m := alpineRegistry.FindStringSubmatch(reg)
			if m == nil {
				return UpstreamSource{}, fmt.Errorf("%s: registryUrl %q — want an Alpine repository, e.g. https://dl-cdn.alpinelinux.org/alpine/v3.22/community", where, reg)
			}
			src.Suite = m[1] + "/" + m[2]
		}
	case "deb":
		src.Apt = dep
		u, err := url.Parse(reg)
		q := u.Query()
		if err != nil || reg == "" || q.Get("suite") == "" {
			return UpstreamSource{}, fmt.Errorf("%s: deb needs registryUrl=https://deb.debian.org/debian?suite=<suite>&components=<component>", where)
		}
		comp := strings.Split(q.Get("components"), ",")[0]
		if comp == "" {
			comp = "main"
		}
		src.Suite = q.Get("suite") + "/" + comp
	case "repology":
		return UpstreamSource{}, fmt.Errorf("%s: datasource=repology is not read — use datasource=apk or datasource=deb, which name the package where it is published", where)
	case "":
		return UpstreamSource{}, fmt.Errorf("%s: renovate annotation names no datasource", where)
	default:
		return UpstreamSource{}, fmt.Errorf("%s: datasource=%s is not an upstream StageFreight lists (pypi, npm, docker, github-releases, github-tags, gitlab-tags, git-tags, apk, deb)", where, ds)
	}
	if ev := f["extractVersion"]; ev != "" {
		src.Select.Tags = ev
	}
	if v := f["versioning"]; v != "" {
		sch, ok := renovateVersioning[strings.SplitN(v, ":", 2)[0]]
		if !ok {
			return UpstreamSource{}, fmt.Errorf("%s: versioning=%s has no StageFreight scheme (semver, pep440, loose, deb)", where, v)
		}
		src.Scheme = sch
	}
	return src, nil
}

// mergeAnnotated adds an annotated source, or its pin to the source the config declares
// under the same id.
func mergeAnnotated(cfg *Config, a UpstreamSource) error {
	for i := range cfg.Upstream.Sources {
		s := &cfg.Upstream.Sources[i]
		if s.ID != a.ID {
			continue
		}
		if s.Annotation != "" {
			return fmt.Errorf("%s: source %q is already declared by the annotation at %s — declare upstream.sources with distinct ids", a.Annotation, a.ID, s.Annotation)
		}
		if k, _ := s.Kind(); k == "" && s.ForkParent == "" {
			s.Git, s.Image, s.PyPI, s.Npm, s.GitHubRelease, s.Apk, s.Apt = a.Git, a.Image, a.PyPI, a.Npm, a.GitHubRelease, a.Apk, a.Apt
			if s.Suite == "" && s.Repository == "" {
				s.Suite, s.Repository, s.registry = a.Suite, a.Repository, a.registry
			}
		}
		if s.Select.Tags == "" {
			s.Select.Tags = a.Select.Tags
		}
		if s.Scheme == "" {
			s.Scheme = a.Scheme
		}
		for _, p := range s.Pins {
			if p.File == a.Pins[0].File && p.Arg == a.Pins[0].Arg {
				s.Annotation = a.Annotation
				return nil
			}
		}
		s.Pins = append(s.Pins, a.Pins...)
		s.Annotation = a.Annotation
		return nil
	}
	cfg.Upstream.Sources = append(cfg.Upstream.Sources, a)
	return nil
}

// RegistryRelease is where an annotated apk source's registryUrl names the Alpine release,
// or nil.
func (s UpstreamSource) RegistryRelease() *UpstreamAnnotation { return s.registry }

// applyUpstreamDefaults fills what the declared sources already say: sync.from is the only git
// source, and a tracked pypi, apk or apt package is probed in every docker build that
// declares no evidence of its own.
func applyUpstreamDefaults(cfg *Config) {
	u := &cfg.Upstream
	if u.Sync.declared() && u.Sync.From == "" {
		var git []string
		for _, s := range u.Sources {
			if k, _ := s.Kind(); k == UpstreamKindGit || s.ForkParent != "" {
				git = append(git, s.ID)
			}
		}
		if len(git) == 1 {
			u.Sync.From = git[0]
		}
	}
	mode, id := u.Version.Mode()
	if mode != UpstreamVersionTrack || u.Version.Ancestry {
		return
	}
	id, _, _ = splitTrackLine(id)
	var probe UpstreamProbe
	for _, s := range u.Sources {
		if s.ID != id {
			continue
		}
		switch kind, loc := s.Kind(); kind {
		case UpstreamKindPyPI:
			probe = UpstreamProbe{Package: loc, Manager: "pip"}
		case UpstreamKindApk:
			probe = UpstreamProbe{Package: loc, Manager: "apk"}
		case UpstreamKindApt:
			probe = UpstreamProbe{Package: loc, Manager: "dpkg"}
		}
	}
	if probe.Package == "" {
		return
	}
	have := map[string]bool{}
	for _, e := range u.Evidence {
		have[e.ID] = true
	}
	for _, b := range cfg.Builds {
		if b.Kind == "docker" && !have[b.ID] {
			u.Evidence = append(u.Evidence, UpstreamEvidence{ID: b.ID, Probe: probe, Derived: true})
		}
	}
}
