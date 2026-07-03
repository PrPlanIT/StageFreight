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
	"github.com/PrPlanIT/StageFreight/src/provision"
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

// Finding is one piece of evidence against a Kustomization. Severity carries its
// authority class and Source its provenance, so operators can distinguish
// "Kubernetes will reject this" (authoritative) from "a community catalog thinks
// this might be wrong" (heuristic). Sources: "graph" (dependsOn integrity),
// "render" (kustomize build / raw-manifest stream), "core-schema" (built-in
// Kubernetes schemas — authoritative), "crd-catalog" (datreeio CRD catalog —
// advisory; see docs/architecture/gitops-fluxcd-validation.md).
type Finding struct {
	Severity Status
	Source   string
	Message  string
	// Schema carries the normalized, provenance-bearing form of a schema-validation
	// finding (core-schema / crd-catalog): the offending field, interpreted rule, and
	// the raw validator transcript for the escape hatch. Nil for non-schema findings
	// (graph/render) — those use Message directly.
	Schema *SchemaFinding
}

// Verdict is the validation outcome for one Flux Kustomization.
type Verdict struct {
	Status   Status
	Findings []Finding
}

// add records a finding and raises the verdict to its severity (never lowers it:
// a heuristic Warn cannot mask an authoritative Fail).
func (v *Verdict) add(f Finding) {
	if f.Severity > v.Status {
		v.Status = f.Severity
	}
	v.Findings = append(v.Findings, f)
}

func (v *Verdict) fail(source, msg string) {
	v.add(Finding{Severity: Fail, Source: source, Message: msg})
}
func (v *Verdict) warn(source, msg string) {
	v.add(Finding{Severity: Warn, Source: source, Message: msg})
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

	kustomizeRes, err := resolveTool(ctx, rootDir, "kustomize", "render Flux kustomizations", desired)
	if err != nil {
		return nil, nil, err
	}
	if kustomizeRes.Path == "" {
		meta.Skipped = "kustomize unavailable (not pinned)"
		return finalize(verdicts), meta, nil
	}
	kubeconformRes, err := resolveTool(ctx, rootDir, "kubeconform", "schema-validate rendered manifests", desired)
	if err != nil {
		return nil, nil, err
	}
	if kubeconformRes.Path == "" {
		meta.Skipped = "kubeconform unavailable (not pinned)"
		return finalize(verdicts), meta, nil
	}
	kustomizeBin, kubeconformBin := kustomizeRes.Path, kubeconformRes.Path
	meta.KustomizeVer, meta.KubeconformVer = kustomizeRes.Version, kubeconformRes.Version

	for path, keys := range byPath {
		absRoot := filepath.Join(rootDir, path)
		rendered, rerr := renderRoot(ctx, kustomizeBin, absRoot)
		if rerr != nil {
			for _, k := range keys {
				verdicts[k].fail("render", fmt.Sprintf("kustomize build failed: %s", rerr.Error()))
			}
			continue
		}
		if len(bytes.TrimSpace(rendered)) == 0 {
			continue
		}
		// schemaCheck returns severity- and source-tagged findings: authoritative
		// core-schema breakage as Fail, advisory CRD-catalog mismatches as Warn.
		for _, f := range schemaCheck(ctx, kubeconformBin, rendered, meta) {
			for _, k := range keys {
				verdicts[k].add(f)
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
		verdicts[key].fail("graph", "in or downstream of a dependsOn cycle")
	}
	for key, missing := range DanglingDeps(graph) {
		for _, m := range missing {
			verdicts[key].fail("graph", fmt.Sprintf("dependsOn references unknown kustomization %s", m))
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
func resolveTool(ctx context.Context, rootDir, tool, purpose string, desired map[string]config.ToolPinConfig) (toolchain.Result, error) {
	ver, pinned := toolchain.ResolveVersion(tool, "", desired)
	res, err := provision.Resolve(ctx, rootDir, tool, ver, purpose) // resolves AND records in the ctx ledger
	if err != nil {
		if pinned {
			return toolchain.Result{}, fmt.Errorf("%s pinned version %s failed to resolve: %w", tool, ver, err)
		}
		return toolchain.Result{}, nil // unavailable + not pinned → advisory skip (empty Path)
	}
	return res, nil
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

// concatManifests joins the YAML files of a raw-manifest directory (a Flux path
// with no kustomization.yaml) into one document stream. It must NOT introduce
// empty documents: a separator before each file (or a file's own leading "---")
// yields a kind-less doc that kubeconform rejects as "missing 'kind' key". So each
// file is trimmed, a leading separator stripped, empties skipped, and the "---"
// separator emitted only BETWEEN real documents.
func concatManifests(dir string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("%s", err.Error())
	}
	var docs [][]byte
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
		b = bytes.TrimSpace(b)
		b = bytes.TrimSpace(bytes.TrimPrefix(b, []byte("---")))
		if len(b) == 0 {
			continue
		}
		docs = append(docs, b)
	}
	return bytes.Join(docs, []byte("\n---\n")), nil
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

// schemaCheck validates rendered manifests as TWO distinct evidence classes with
// different authority — not one undifferentiated pass/fail stream:
//
//  1. Authoritative (built-in Kubernetes schemas): a failure here means the API
//     server / Flux will reject the resource. Emitted as a Fail finding
//     (source "core-schema"). CRDs have no built-in schema and are skipped here.
//  2. Heuristic (datreeio CRD catalog): community, OpenAPI-derived schemas that
//     are routinely stricter-than-reality (e.g. a Vault CR the operator accepts
//     but whose embedded corev1.Container marks `name` required). A failure here
//     is advisory — emitted as a Warn finding (source "crd-catalog"), never a Fail.
//
// Keeping these separate is the trust boundary: it must NOT be re-collapsed into a
// single pass as an "optimization". The two passes share an identical input, so
// kubeconform reports resources in the same order — they are zipped by index to
// merge coverage (a kind is "no schema" only when BOTH passes skip it).
func schemaCheck(ctx context.Context, kubeconformBin string, rendered []byte, meta *ValidationMeta) []Finding {
	core, cerr := kubeconformPass(ctx, kubeconformBin, rendered, "default")
	if cerr != nil {
		// Authoritative pass failed to produce parseable output — a render-level fail.
		return []Finding{{Severity: Fail, Source: "core-schema", Message: fmt.Sprintf("kubeconform output unparseable: %s", cerr.Error())}}
	}
	// Heuristic pass is best-effort: if the catalog is unreachable we lose advisory
	// evidence but never fail on it, so a pass error degrades to "no catalog schema".
	cat, _ := kubeconformPass(ctx, kubeconformBin, rendered, datreeCatalog)

	var findings []Finding
	for i, c := range core {
		kind := kindOf(c)
		switch classifyStatus(c.Status) {
		case "invalid", "error":
			sf := parseSchemaViolation(c.Kind, c.Name, c.Version, c.Msg)
			findings = append(findings, Finding{Severity: Fail, Source: "core-schema",
				Message: fmt.Sprintf("%s/%s (%s): %s", c.Kind, c.Name, c.Version, strings.TrimSpace(c.Msg)),
				Schema:  &sf})
		case "valid":
			meta.Validated[kind]++
		case "skipped":
			// No authoritative schema — consult the heuristic catalog (same index).
			if i >= len(cat) {
				meta.NoSchema[kind]++
				continue
			}
			t := cat[i]
			switch classifyStatus(t.Status) {
			case "invalid", "error":
				sf := parseSchemaViolation(t.Kind, t.Name, t.Version, t.Msg)
				findings = append(findings, Finding{Severity: Warn, Source: "crd-catalog",
					Message: fmt.Sprintf("%s/%s (%s): %s", t.Kind, t.Name, t.Version, strings.TrimSpace(t.Msg)),
					Schema:  &sf})
			case "valid":
				meta.Validated[kind]++
			default:
				meta.NoSchema[kind]++ // skipped by both — no schema anywhere
			}
		}
	}
	return findings
}

// kubeconformPass runs one kubeconform invocation against a single schema location
// and returns the per-resource results. A non-zero exit (invalid resources) is a
// normal, parseable outcome; only unparseable output is an error.
func kubeconformPass(ctx context.Context, kubeconformBin string, rendered []byte, schemaLocation string) ([]kcResource, error) {
	cmd := exec.CommandContext(ctx, kubeconformBin,
		"-ignore-missing-schemas",
		"-verbose",
		"-output", "json",
		"-schema-location", schemaLocation,
		"-",
	)
	cmd.Stdin = bytes.NewReader(rendered)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()

	var out kcOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return out.Resources, nil
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
