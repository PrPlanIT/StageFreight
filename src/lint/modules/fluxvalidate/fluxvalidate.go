// Package fluxvalidate is a repository-scoped lint module that renders a repo's
// Flux build roots with kustomize and schema-validates the output with
// kubeconform — fully offline, no cluster credentials. It activates on CONTENT:
// if the repository contains no Flux Kustomization resources it does nothing, so
// a pure build repo never pays for it while a repo that carries Flux manifests
// (in any lifecycle mode) gets them validated. The diagnosis is the value — a
// finding names the kind, object, and the exact schema violation.
//
// Phase 1 is advisory: findings surface but enforcement (which proofs are
// required to gate audition) is a separate, deliberate design. See
// docs/architecture/audition-proofs.md when that lands.
package fluxvalidate

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
	"github.com/PrPlanIT/StageFreight/src/gitops"
	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

const moduleName = "flux-validate"

// datreeCatalog gives kubeconform real schemas for the CRDs a Flux estate is
// built from (HelmRelease, Kustomization, cert-manager, Istio, …). Resources
// absent from the catalog are reported as coverage gaps, never silently dropped.
const datreeCatalog = "https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json"

func init() {
	lint.RegisterRepository(moduleName, func() lint.RepositoryModule { return &module{} })
}

type module struct {
	desired map[string]config.ToolPinConfig
}

func (m *module) Name() string       { return moduleName }
func (m *module) DefaultEnabled() bool { return true }

// SetToolchainDesired satisfies the engine's toolchain-aware probe.
func (m *module) SetToolchainDesired(desired map[string]config.ToolPinConfig) {
	m.desired = desired
}

// CheckRepository discovers the Flux build roots and validates each one. It is
// inert (returns no findings, resolves no tools) when the repo has no Flux
// content. Tool-resolution failures degrade gracefully unless the tool version
// was explicitly pinned — a network blip should not hard-fail an advisory check.
func (m *module) CheckRepository(ctx context.Context, root string) ([]lint.Finding, error) {
	// Activation is CONTENT-based, not mode-based: the module runs wherever Flux
	// Kustomization resources exist (multi-modal — a build repo may also carry
	// Flux manifests), and is inert otherwise. KNOWN LIMITATION (revisit before
	// enforcement): this walks the whole tree and does NOT yet honor the lint
	// engine's exclude paths, so Flux CRs living in testdata/, examples/, docs/,
	// or templates/ would activate validation and surface advisory findings.
	// Harmless while advisory; under required-proof enforcement (Phase 2) repo
	// modules must receive and respect the engine's excludes so fixtures can't
	// fail a gate. See docs/architecture/audition-proofs.md.
	graph, err := gitops.DiscoverFluxGraph(root)
	if err != nil {
		return nil, fmt.Errorf("discovering flux graph: %w", err)
	}
	roots := gitops.BuildRoots(graph)
	if len(roots) == 0 {
		return nil, nil // no Flux content — module is inert
	}

	kustomizeBin, err := m.resolve(root, "kustomize")
	if err != nil {
		return nil, err
	}
	if kustomizeBin == "" {
		return []lint.Finding{skipped("kustomize")}, nil
	}
	kubeconformBin, err := m.resolve(root, "kubeconform")
	if err != nil {
		return nil, err
	}
	if kubeconformBin == "" {
		return []lint.Finding{skipped("kubeconform")}, nil
	}

	var findings []lint.Finding
	noSchema := map[string]int{}
	validated := 0

	for _, r := range roots {
		absRoot := filepath.Join(root, r)
		rendered, rerr := m.render(ctx, kustomizeBin, absRoot)
		if rerr != nil {
			findings = append(findings, lint.Finding{
				File:     r,
				Module:   moduleName,
				Severity: lint.SeverityCritical,
				Message:  fmt.Sprintf("kustomize build failed: %s", rerr.Error()),
			})
			continue
		}
		if len(bytes.TrimSpace(rendered)) == 0 {
			continue
		}
		fs, ok := m.schemaCheck(ctx, kubeconformBin, r, rendered, noSchema)
		findings = append(findings, fs...)
		validated += ok
	}

	// Coverage gaps — advisory, aggregated by kind across the whole repo so a
	// CRD that has no schema reads as one line, not one per occurrence.
	for _, kind := range sortedKinds(noSchema) {
		findings = append(findings, lint.Finding{
			Module:   moduleName,
			Severity: lint.SeverityInfo,
			Message:  fmt.Sprintf("no schema for %s (%d) — validation coverage gap", kind, noSchema[kind]),
		})
	}
	// Positive confirmation of what was actually checked.
	if validated > 0 {
		findings = append(findings, lint.Finding{
			Module:   moduleName,
			Severity: lint.SeverityInfo,
			Message:  fmt.Sprintf("validated %d resource(s) across %d build root(s)", validated, len(roots)),
		})
	}

	return findings, nil
}

// resolve returns the tool's binary path. An empty path with a nil error means
// the tool is unavailable and was not pinned (caller emits an advisory skip); a
// non-nil error means a pinned version failed to resolve (hard failure).
//
// ENFORCEMENT HAZARD (Phase 2): the empty-path "skip" branch is safe ONLY while
// flux-validate is advisory. The moment it becomes an enforceable proof
// (audition.required: [flux-validate]), a tool-resolution failure here would
// produce no findings → gate passes → a silent validation bypass. When wiring
// enforcement, a skip-due-to-unavailable-tool MUST become a proof FAILURE, not a
// pass. See docs/architecture/audition-proofs.md.
func (m *module) resolve(root, tool string) (string, error) {
	ver, pinned := toolchain.ResolveVersion(tool, "", m.desired)
	res, err := toolchain.Resolve(root, tool, ver)
	if err != nil {
		if pinned {
			return "", fmt.Errorf("%s pinned version %s failed to resolve: %w", tool, ver, err)
		}
		return "", nil
	}
	return res.Path, nil
}

// render produces the manifests a Flux build root resolves to. A directory with
// a kustomization entrypoint is rendered with `kustomize build` under Flux's
// load restrictions (so ../base overlays resolve as the controller renders
// them); a plain manifest directory is concatenated as-is, mirroring how Flux
// accepts non-kustomize paths.
func (m *module) render(ctx context.Context, kustomizeBin, dir string) ([]byte, error) {
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

// schemaCheck pipes rendered manifests through kubeconform and folds the verdicts
// into findings. Returns the findings plus the count of resources validated
// against a known schema. Resources with no schema increment noSchema (reported
// as coverage gaps by the caller) rather than failing.
func (m *module) schemaCheck(ctx context.Context, kubeconformBin, root string, rendered []byte, noSchema map[string]int) ([]lint.Finding, int) {
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
	// infrastructure failure. Only unparseable output is an error.
	_ = cmd.Run()

	var out kcOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return []lint.Finding{{
			File:     root,
			Module:   moduleName,
			Severity: lint.SeverityCritical,
			Message:  fmt.Sprintf("kubeconform output unparseable: %s", msg),
		}}, 0
	}

	var findings []lint.Finding
	validated := 0
	for _, res := range out.Resources {
		switch classifyStatus(res.Status) {
		case "invalid", "error":
			findings = append(findings, lint.Finding{
				File:     root,
				Module:   moduleName,
				Severity: lint.SeverityCritical,
				Message:  fmt.Sprintf("%s/%s (%s): %s", res.Kind, res.Name, res.Version, strings.TrimSpace(res.Msg)),
			})
		case "skipped":
			kind := res.Kind
			if kind == "" {
				kind = "(unknown)"
			}
			noSchema[kind]++
		case "valid":
			validated++
		}
	}
	return findings, validated
}

func skipped(tool string) lint.Finding {
	return lint.Finding{
		Module:   moduleName,
		Severity: lint.SeverityInfo,
		Message:  fmt.Sprintf("skipped: %s unavailable (not pinned) — Flux manifests not validated", tool),
	}
}

func hasKustomization(dir string) bool {
	for _, name := range []string{"kustomization.yaml", "kustomization.yml", "Kustomization"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// concatManifests joins every YAML document in dir (non-recursive — a Flux path
// is one render unit) with document separators for plain-manifest roots.
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

// kcResource mirrors one entry of kubeconform's verbose JSON output.
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

func sortedKinds(m map[string]int) []string {
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
