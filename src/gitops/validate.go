package gitops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// GitOps (Flux) manifest validation. The unit of truth is the Flux Kustomization
// (KustomizationKey): validation produces a verdict per Kustomization, not per
// file and not for the repository as a whole. Build paths are the validation
// INPUT — a path is rendered once and its result attributed to every
// Kustomization that consumes it. Graph-integrity checks (cycles, dangling
// dependsOn) attribute back to the implicated Kustomizations too. See
// docs/architecture/gitops-fluxcd-validation.md.
//
// Activation is content-based: a repo with no Flux Kustomizations yields an empty
// verdict map. Validation is fully hermetic (kustomize + kubeconform, no cluster).

// Status is a per-Kustomization verdict outcome.
type Status int

const (
	Pass Status = iota
	Warn
	Fail
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "pass"
	case Warn:
		return "warn"
	case Fail:
		return "fail"
	default:
		return fmt.Sprintf("status(%d)", int(s))
	}
}

// Verdict is the validation outcome for one Flux Kustomization.
type Verdict struct {
	Status  Status
	Reasons []string
}

func (v *Verdict) fail(reason string) {
	v.Status = Fail
	v.Reasons = append(v.Reasons, reason)
}

// ValidationMeta carries repository-scoped facts that are not per-Kustomization
// verdicts: tool versions, a skip reason, and schema coverage (kinds checked vs
// kinds with no available schema). Coverage gaps are reported, never failed.
type ValidationMeta struct {
	Roots          int
	Skipped        string // non-empty: validation could not run (e.g. tool unavailable)
	KustomizeVer   string
	KubeconformVer string
	Validated      map[string]int // kind -> count checked against a known schema
	NoSchema       map[string]int // kind -> count with no available schema
}

const datreeCatalog = "https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json"

// ValidateManifests discovers the Flux graph and returns a verdict per
// Kustomization plus repository-scoped meta. The error return is reserved for
// infrastructure failures (graph discovery, pinned tool resolution); a repo with
// no Flux content returns an empty map and zero-roots meta.
func ValidateManifests(ctx context.Context, rootDir string, desired map[string]config.ToolPinConfig) (map[KustomizationKey]Verdict, *ValidationMeta, error) {
	graph, err := DiscoverFluxGraph(rootDir)
	if err != nil {
		return nil, nil, fmt.Errorf("discovering flux graph: %w", err)
	}
	meta := &ValidationMeta{Validated: map[string]int{}, NoSchema: map[string]int{}}
	if len(graph.Kustomizations) == 0 {
		return map[KustomizationKey]Verdict{}, meta, nil // no Flux content — inert
	}

	verdicts := graphVerdicts(graph)

	// ── structural proofs (render + schema), per unique build path ────────────
	byPath := map[string][]KustomizationKey{}
	for key, node := range graph.Kustomizations {
		if node.Path != "" {
			byPath[node.Path] = append(byPath[node.Path], key)
		}
	}
	meta.Roots = len(byPath)
	if len(byPath) == 0 {
		return finalize(verdicts), meta, nil
	}

	kustomizeBin, kustomizeVer, err := resolveTool(rootDir, "kustomize", desired)
	if err != nil {
		return nil, nil, err
	}
	if kustomizeBin == "" {
		meta.Skipped = "kustomize unavailable (not pinned)"
		return finalize(verdicts), meta, nil
	}
	kubeconformBin, kubeconformVer, err := resolveTool(rootDir, "kubeconform", desired)
	if err != nil {
		return nil, nil, err
	}
	if kubeconformBin == "" {
		meta.Skipped = "kubeconform unavailable (not pinned)"
		return finalize(verdicts), meta, nil
	}
	meta.KustomizeVer, meta.KubeconformVer = kustomizeVer, kubeconformVer

	for path, keys := range byPath {
		absRoot := filepath.Join(rootDir, path)
		rendered, rerr := renderRoot(ctx, kustomizeBin, absRoot)
		if rerr != nil {
			for _, k := range keys {
				verdicts[k].fail(fmt.Sprintf("kustomize build failed: %s", rerr.Error()))
			}
			continue
		}
		if len(bytes.TrimSpace(rendered)) == 0 {
			continue
		}
		invalid := schemaCheck(ctx, kubeconformBin, rendered, meta)
		for _, msg := range invalid {
			for _, k := range keys {
				verdicts[k].fail(msg)
			}
		}
	}

	return finalize(verdicts), meta, nil
}

// graphVerdicts seeds a Pass verdict per kustomization and applies the
// graph-integrity proofs (dependsOn cycles, dangling dependsOn references),
// attributing each failure to the implicated kustomization(s). Structural
// (render/schema) proofs are layered on top by the caller.
func graphVerdicts(graph *FluxGraph) map[KustomizationKey]*Verdict {
	verdicts := make(map[KustomizationKey]*Verdict, len(graph.Kustomizations))
	for key := range graph.Kustomizations {
		verdicts[key] = &Verdict{Status: Pass}
	}
	for key := range CycleNodes(graph) {
		verdicts[key].fail("in or downstream of a dependsOn cycle")
	}
	for key, missing := range DanglingDeps(graph) {
		for _, m := range missing {
			verdicts[key].fail(fmt.Sprintf("dependsOn references unknown kustomization %s", m))
		}
	}
	return verdicts
}

func finalize(v map[KustomizationKey]*Verdict) map[KustomizationKey]Verdict {
	out := make(map[KustomizationKey]Verdict, len(v))
	for k, ver := range v {
		out[k] = *ver
	}
	return out
}

// resolveTool returns the tool's binary path + resolved version. An empty path
// with a nil error means the tool is unavailable and was not pinned (advisory
// skip); a non-nil error means a pinned version failed to resolve (hard).
//
// ENFORCEMENT HAZARD (audition-proofs / Increment 4): the empty-path "skip" is
// safe ONLY while this validation is advisory. If a Flux verdict ever gates a
// merge (required), a tool-resolution failure here would produce no Fail verdicts
// → a silent bypass. Under enforcement, an unavailable tool MUST become a Fail,
// not a skip.
func resolveTool(rootDir, tool string, desired map[string]config.ToolPinConfig) (string, string, error) {
	ver, pinned := toolchain.ResolveVersion(tool, "", desired)
	res, err := toolchain.Resolve(rootDir, tool, ver)
	if err != nil {
		if pinned {
			return "", "", fmt.Errorf("%s pinned version %s failed to resolve: %w", tool, ver, err)
		}
		return "", "", nil
	}
	return res.Path, res.Version, nil
}

// renderRoot renders a build path. A directory with a kustomize entrypoint is
// rendered with Flux's load restrictions so ../base overlays resolve as the
// controller renders them; a plain manifest directory is concatenated as-is.
func renderRoot(ctx context.Context, kustomizeBin, dir string) ([]byte, error) {
	if hasKustomization(dir) {
		cmd := exec.CommandContext(ctx, kustomizeBin, "build", dir, "--load-restrictor=LoadRestrictionsNone")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return nil, fmt.Errorf("%s", msg)
		}
		return stdout.Bytes(), nil
	}
	return concatManifests(dir)
}

func hasKustomization(dir string) bool {
	for _, name := range []string{"kustomization.yaml", "kustomization.yml", "Kustomization"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

func concatManifests(dir string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("%s", err.Error())
	}
	var buf bytes.Buffer
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".yaml") && !strings.HasSuffix(n, ".yml") {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(dir, n))
		if rerr != nil {
			continue
		}
		buf.WriteString("\n---\n")
		buf.Write(b)
	}
	return buf.Bytes(), nil
}

type kcResource struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status"`
	Msg     string `json:"msg"`
}

type kcOutput struct {
	Resources []kcResource `json:"resources"`
}

// schemaCheck pipes rendered manifests through kubeconform and returns the
// human-readable messages for invalid/errored resources. Validated and
// no-schema counts are folded into meta (coverage), not into the return.
func schemaCheck(ctx context.Context, kubeconformBin string, rendered []byte, meta *ValidationMeta) []string {
	cmd := exec.CommandContext(ctx, kubeconformBin,
		"-ignore-missing-schemas",
		"-verbose",
		"-output", "json",
		"-schema-location", "default",
		"-schema-location", datreeCatalog,
		"-",
	)
	cmd.Stdin = bytes.NewReader(rendered)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// A non-zero exit means invalid resources — a normal result we parse, not an
	// infrastructure failure. Unparseable output is reported as a render-level fail.
	_ = cmd.Run()

	var out kcOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return []string{fmt.Sprintf("kubeconform output unparseable: %s", msg)}
	}

	var invalid []string
	for _, res := range out.Resources {
		switch classifyStatus(res.Status) {
		case "invalid", "error":
			invalid = append(invalid, fmt.Sprintf("%s/%s (%s): %s", res.Kind, res.Name, res.Version, strings.TrimSpace(res.Msg)))
		case "skipped":
			meta.NoSchema[kindOf(res)]++
		case "valid":
			meta.Validated[kindOf(res)]++
		}
	}
	return invalid
}

func kindOf(r kcResource) string {
	if r.Kind == "" {
		return "(unknown)"
	}
	return r.Kind
}

// classifyStatus maps kubeconform's status strings to a stable verdict. Checks
// "invalid" before "valid" because the former contains the latter as a substring.
func classifyStatus(status string) string {
	s := strings.ToLower(status)
	switch {
	case strings.Contains(s, "invalid"):
		return "invalid"
	case strings.Contains(s, "error"):
		return "error"
	case strings.Contains(s, "skipped"):
		return "skipped"
	case strings.Contains(s, "valid"):
		return "valid"
	default:
		return "other"
	}
}

// SortedKinds returns the kinds of a count map in descending count, then name.
func SortedKinds(m map[string]int) []string {
	kinds := make([]string, 0, len(m))
	for k := range m {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool {
		if m[kinds[i]] != m[kinds[j]] {
			return m[kinds[i]] > m[kinds[j]]
		}
		return kinds[i] < kinds[j]
	})
	return kinds
}
