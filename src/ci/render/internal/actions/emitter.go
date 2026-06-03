// Package actions is a private serialization backend: it writes a forge-neutral
// model.Pipeline out in the GitHub Actions workflow wire format.
//
// It is mechanism, not identity. It lives under ci/render/internal so it can be
// imported only by the render layer and never appears in any user-facing surface
// (CLI, config, docs, output). A forge emitter that happens to use this wire
// format calls Emit with a Dialect carrying that forge's provider identity; the
// backend itself names no forge and asserts no equivalence between forges. When a
// forge's needs diverge from this format, that forge's package owns the
// divergence — this backend does not grow forge-specific branches.
//
// Lowering decisions (the gaps a stage-based forge hides that Actions makes
// explicit):
//   - stages → none exist; a job with no explicit Needs is wired to depend on
//     every job in the immediately-preceding stage, preserving ordering as an
//     explicit needs DAG.
//   - artifacts → flow is explicit: a producer uploads, every downstream consumer
//     that needs it downloads.
//   - OIDC → permissions: id-token: write plus a step that requests a token with
//     audience "stagefreight" and exports it as STAGEFREIGHT_OIDC.
//   - docker → a docker:dind service plus DOCKER_HOST/TLS env.
package actions

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/ci/render/model"
)

// Dialect carries the per-call values a forge emitter supplies. Only the values
// that legitimately vary between callers belong here; the backend reads them but
// chooses nothing about identity itself.
type Dialect struct {
	// Provider is the forge identity string. It is written verbatim into the
	// header banner and SF_CI_PROVIDER so the rendered document and the runtime
	// context both report the calling forge, not the backend.
	Provider string
}

// Emit serializes a forge-neutral Pipeline to Actions workflow bytes using the
// given dialect. Pure and deterministic: identical (p, d) → identical bytes.
func Emit(p model.Pipeline, d Dialect) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(header(d.Provider))

	buf.WriteString("\nname: stagefreight\n")

	// ── triggers ─────────────────────────────────────────────────────────────
	buf.WriteString("\non:\n")
	buf.WriteString("  push:\n")
	buf.WriteString("  pull_request:\n")
	buf.WriteString("  workflow_dispatch:\n")

	// ── concurrency (cancel superseded) ──────────────────────────────────────
	if p.Defaults.CancelSuperseded || p.Defaults.Interruptible {
		buf.WriteString("\nconcurrency:\n")
		buf.WriteString("  group: stagefreight-${{ github.workflow }}-${{ github.ref }}\n")
		buf.WriteString("  cancel-in-progress: true\n")
	}

	// ── least-privilege default token; OIDC jobs widen at job scope ──────────
	buf.WriteString("\npermissions:\n")
	buf.WriteString("  contents: read\n")

	stages := orderStages(p)
	producers := map[string]bool{}
	for _, j := range p.Jobs {
		if len(j.Artifacts.Paths) > 0 {
			producers[j.Name] = true
		}
	}

	buf.WriteString("\njobs:\n")
	for i, j := range p.Jobs {
		if i > 0 {
			buf.WriteString("\n")
		}
		emitJob(&buf, j, p.Defaults, d.Provider, stages, producers)
	}

	return buf.Bytes(), nil
}

func emitJob(buf *bytes.Buffer, j model.Job, def model.PipelineDefaults, provider string, stages stageOrder, producers map[string]bool) {
	needs := effectiveNeeds(j, stages)

	fmt.Fprintf(buf, "  %s:\n", j.Name)

	// runs-on (routing). No labels → ubuntu-latest, the default label every
	// Actions runner (hosted or self-hosted) provides.
	if len(j.Routing.Labels) > 0 {
		fmt.Fprintf(buf, "    runs-on: [%s]\n", strings.Join(j.Routing.Labels, ", "))
	} else {
		buf.WriteString("    runs-on: ubuntu-latest\n")
	}

	// container image
	if def.Image != "" {
		buf.WriteString("    container:\n")
		fmt.Fprintf(buf, "      image: %s\n", def.Image)
	}

	// needs (explicit, or derived from the preceding stage)
	if len(needs) > 0 {
		fmt.Fprintf(buf, "    needs: [%s]\n", strings.Join(needs, ", "))
	}

	// run regardless of upstream outcome (GitLab when: always). In Actions a job
	// with needs is skipped if any need failed unless it opts in with always().
	if j.Policy.WhenAlways {
		buf.WriteString("    if: ${{ always() }}\n")
	}

	// allow failure (this job's own failure does not fail the run)
	if j.Policy.AllowFailure {
		buf.WriteString("    continue-on-error: true\n")
	}

	// OIDC token permission (widen the default token for this job only)
	if j.Capabilities.OIDC {
		buf.WriteString("    permissions:\n")
		buf.WriteString("      contents: read\n")
		buf.WriteString("      id-token: write\n")
	}

	// docker (DinD service + transport env)
	if j.Capabilities.Docker {
		buf.WriteString("    env:\n")
		buf.WriteString("      DOCKER_HOST: tcp://dind:2376\n")
		buf.WriteString("      DOCKER_TLS_VERIFY: \"1\"\n")
		buf.WriteString("      DOCKER_CERT_PATH: /certs/client\n")
		buf.WriteString("    services:\n")
		buf.WriteString("      dind:\n")
		buf.WriteString("        image: docker:dind\n")
		buf.WriteString("        options: --privileged\n")
	}

	buf.WriteString("    steps:\n")

	// checkout (full clone when requested)
	buf.WriteString("      - uses: actions/checkout@v4\n")
	if j.Source.FullClone {
		buf.WriteString("        with:\n")
		buf.WriteString("          fetch-depth: 0\n")
	}

	// OIDC: request a token with the stagefreight audience and export it as the
	// env the binary reads (STAGEFREIGHT_OIDC). wget (busybox) for image
	// portability — the StageFreight CI image is Alpine and has no curl.
	if j.Capabilities.OIDC {
		buf.WriteString("      - name: stagefreight-oidc\n")
		buf.WriteString("        run: |\n")
		buf.WriteString("          token=$(wget -qO- --header=\"Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN\" \\\n")
		buf.WriteString("            \"$ACTIONS_ID_TOKEN_REQUEST_URL&audience=stagefreight\" | sed -n 's/.*\"value\":\"\\([^\"]*\\)\".*/\\1/p')\n")
		buf.WriteString("          echo \"STAGEFREIGHT_OIDC=$token\" >> \"$GITHUB_ENV\"\n")
	}

	// CI context hydration (SF_CI_* from the runtime context)
	if def.CIContext {
		buf.WriteString("      - name: stagefreight-context\n")
		buf.WriteString("        run: |\n")
		buf.WriteString("          {\n")
		for _, line := range ciContextExports(provider) {
			fmt.Fprintf(buf, "            %s\n", line)
		}
		buf.WriteString("          } >> \"$GITHUB_ENV\"\n")
	}

	// download artifacts from each upstream producer this job depends on
	for _, n := range needs {
		if producers[n] {
			buf.WriteString("      - uses: actions/download-artifact@v4\n")
			buf.WriteString("        with:\n")
			fmt.Fprintf(buf, "          name: %s\n", n)
			buf.WriteString("          path: .\n")
		}
	}

	// the phase command(s)
	fmt.Fprintf(buf, "      - name: %s\n", j.Name)
	buf.WriteString("        run: |\n")
	for _, cmd := range j.Commands {
		fmt.Fprintf(buf, "          %s\n", cmd)
	}

	// upload this job's artifacts for downstream consumers
	if len(j.Artifacts.Paths) > 0 {
		buf.WriteString("      - uses: actions/upload-artifact@v4\n")
		if j.Policy.WhenAlways {
			buf.WriteString("        if: always()\n")
		}
		buf.WriteString("        with:\n")
		fmt.Fprintf(buf, "          name: %s\n", j.Name)
		buf.WriteString("          path: |\n")
		for _, pth := range j.Artifacts.Paths {
			fmt.Fprintf(buf, "            %s\n", pth)
		}
		if days := retentionDays(j.Artifacts.ExpireIn); days != "" {
			fmt.Fprintf(buf, "          retention-days: %s\n", days)
		}
	}
}

// header is the provider-stamped banner — same shape as every StageFreight
// skeleton header, so each forge reads as a first-class generated document.
func header(provider string) string {
	return fmt.Sprintf(`# stagefreight-skeleton: universal
# provider: %s
# supports: audition, perform, review, publish, narrate
#
# GENERATED BY STAGEFREIGHT — DO NOT EDIT
# Regenerate: stagefreight ci render %s --write
#
# Universal lifecycle transport. One skeleton for all repo modes.
# StageFreight resolves modality from lifecycle.mode in .stagefreight.yml.
`, provider, provider)
}

// ciContextExports returns the SF_CI_* lines for the Actions runtime context.
// SF_CI_PROVIDER carries the calling forge so the binary treats each forge as
// itself; the remaining values come from the github.* context the Actions
// runtime exposes.
func ciContextExports(provider string) []string {
	return []string{
		fmt.Sprintf(`echo "SF_CI_PROVIDER=%s"`, provider),
		`echo "SF_CI_EVENT=${{ github.event_name }}"`,
		`echo "SF_CI_BRANCH=${{ github.ref_name }}"`,
		`echo "SF_CI_TAG=${{ github.ref_type == 'tag' && github.ref_name || '' }}"`,
		`echo "SF_CI_SHA=${{ github.sha }}"`,
		`echo "SF_CI_DEFAULT_BRANCH=${{ github.event.repository.default_branch }}"`,
		`echo "SF_CI_REPO_URL=${{ github.server_url }}/${{ github.repository }}"`,
		`echo "SF_CI_WORKSPACE=$PWD"`,
		`echo "SF_CI_PIPELINE_ID=${{ github.run_id }}"`,
	}
}

// stageOrder captures stage names in first-seen order and the jobs in each.
type stageOrder struct {
	order   []string
	byStage map[string][]string
}

func orderStages(p model.Pipeline) stageOrder {
	s := stageOrder{byStage: map[string][]string{}}
	for _, j := range p.Jobs {
		if _, seen := s.byStage[j.Stage]; !seen {
			s.order = append(s.order, j.Stage)
		}
		s.byStage[j.Stage] = append(s.byStage[j.Stage], j.Name)
	}
	return s
}

// effectiveNeeds returns explicit Needs, or — for a job that declares none — the
// jobs in the immediately-preceding stage, so stage ordering becomes an explicit
// DAG. First-stage jobs get no dependencies.
func effectiveNeeds(j model.Job, s stageOrder) []string {
	if len(j.Needs) > 0 {
		return j.Needs
	}
	idx := -1
	for i, name := range s.order {
		if name == j.Stage {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return nil
	}
	return s.byStage[s.order[idx-1]]
}

// retentionDays converts a human duration ("1 week", "2 hours", "1 day") to an
// integer day count for actions/upload-artifact retention-days (min 1). Unknown
// or empty input → "" (forge default retention).
func retentionDays(expireIn string) string {
	f := strings.Fields(strings.ToLower(strings.TrimSpace(expireIn)))
	if len(f) != 2 {
		return ""
	}
	n, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return ""
	}
	var days float64
	switch {
	case strings.HasPrefix(f[1], "hour"):
		days = n / 24
	case strings.HasPrefix(f[1], "day"):
		days = n
	case strings.HasPrefix(f[1], "week"):
		days = n * 7
	case strings.HasPrefix(f[1], "month"):
		days = n * 30
	default:
		return ""
	}
	d := int(math.Ceil(days))
	if d < 1 {
		d = 1
	}
	return strconv.Itoa(d)
}
