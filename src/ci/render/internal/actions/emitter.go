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
//   - docker → NOT injected. The build engine is deferred to the runner (mounted
//     socket on hosted; dind/buildkitd via DOCKER_HOST/BUILDKIT_HOST on self-hosted),
//     auto-detected by the runtime. The workflow injects no transport or dind service.
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

	// NativeRegistries are the config registry providers this forge can authenticate
	// with its auto-token (e.g. github → "ghcr","github"). A job that pushes to one of
	// them gets PackageAuth wired; others are left to explicit secrets.
	NativeRegistries []string

	// PackageAuth, when non-nil, is the forge's recipe for authenticating to its
	// native package/container registry. The backend emits it verbatim — it names no
	// forge and chooses nothing; the forge package decides whether its registry has a
	// turnkey token and which providers count as native.
	PackageAuth *PackageAuth
}

// PackageAuth is a forge's package-registry auth recipe: the permission that widens
// the job token, plus the credential VALUE expressions the forge runtime provides
// (e.g. GitHub's `${{ github.actor }}` / `${{ secrets.GITHUB_TOKEN }}`). User and
// Token are emitted as `<prefix>_USER` / `<prefix>_TOKEN` from the job's capability.
type PackageAuth struct {
	Permission string
	User       string
	Token      string
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
		emitJob(&buf, j, p.Defaults, d, stages, producers)
	}

	return buf.Bytes(), nil
}

func emitJob(buf *bytes.Buffer, j model.Job, def model.PipelineDefaults, d Dialect, stages stageOrder, producers map[string]bool) {
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

	// Resolve the package-registry credential prefix this forge auto-authenticates
	// for this job: the first capability registry whose provider the forge owns.
	pkgPrefix := ""
	if d.PackageAuth != nil {
		for _, pr := range j.Capabilities.PackageRegistries {
			if containsStr(d.NativeRegistries, pr.Provider) {
				pkgPrefix = pr.CredPrefix
				break
			}
		}
	}
	pkg := pkgPrefix != ""

	// permissions (job-scope override; restate contents:read since the override
	// replaces the workflow default). OIDC widens with id-token; a package-registry
	// push widens with the forge's package permission. Collected so both compose.
	if j.Capabilities.OIDC || pkg {
		buf.WriteString("    permissions:\n")
		buf.WriteString("      contents: read\n")
		if j.Capabilities.OIDC {
			buf.WriteString("      id-token: write\n")
		}
		if pkg {
			fmt.Fprintf(buf, "      %s\n", d.PackageAuth.Permission)
		}
	}

	// Build engine: NOT injected. The Actions family DEFERS build-engine provisioning
	// to the runner. Hosted runners expose the mounted docker socket; self-hosted
	// runners expose dind/buildkitd via DOCKER_HOST / BUILDKIT_HOST. StageFreight's
	// runtime auto-detects whichever is present (BUILDKIT_HOST → DOCKER_HOST → local
	// socket), so the workflow stays thin and the runner owns engine quality. A
	// workflow-injected dind would shadow a self-hosted operator's buildkitd AND fail
	// in hosted container jobs (sibling service hostnames don't resolve), so we don't.
	// (The image still rides perform→publish as an OCI layout in .stagefreight/ via
	// artifacts — daemon-independent, so an ephemeral hosted builder is safe.)

	buf.WriteString("    steps:\n")

	// CI context FIRST (SF_CI_* from the runtime context) — the checkout step below
	// reads SF_CI_REPO_URL / SF_CI_SHA / branch from it.
	if def.CIContext {
		buf.WriteString("      - name: stagefreight-context\n")
		buf.WriteString("        run: |\n")
		buf.WriteString("          {\n")
		for _, line := range ciContextExports(d.Provider) {
			fmt.Fprintf(buf, "            %s\n", line)
		}
		buf.WriteString("          } >> \"$GITHUB_ENV\"\n")
	}

	// checkout: StageFreight clones the repo itself via go-git. The image carries no
	// git binary, so actions/checkout would fall back to a .git-less REST tarball;
	// go-git materializes a real .git instead — and is ownership-agnostic, so no
	// safe.directory dance. Auth from the runner-provided GITHUB_TOKEN.
	buf.WriteString("      - name: stagefreight-checkout\n")
	buf.WriteString("        env:\n")
	buf.WriteString("          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}\n")
	buf.WriteString("        run: stagefreight ci checkout\n")

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

	// download artifacts from each upstream producer this job depends on
	for _, n := range needs {
		if producers[n] {
			buf.WriteString("      - uses: actions/download-artifact@v4\n")
			buf.WriteString("        with:\n")
			fmt.Fprintf(buf, "          name: %s\n", n)
			buf.WriteString("          path: .\n")
		}
	}

	// the phase command(s). No Docker transport is injected — the runtime auto-detects
	// the runner-provided engine. Package-registry creds (registry auth, not transport)
	// are scoped to this step so they reach the phase command and nothing else.
	fmt.Fprintf(buf, "      - name: %s\n", j.Name)
	if pkg {
		buf.WriteString("        env:\n")
		fmt.Fprintf(buf, "          %s_USER: %s\n", pkgPrefix, d.PackageAuth.User)
		fmt.Fprintf(buf, "          %s_TOKEN: %s\n", pkgPrefix, d.PackageAuth.Token)
	}
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
		// .stagefreight/ is a dot-directory; upload-artifact v4.4.0+ excludes hidden
		// files (anything under a path beginning with ".") by default, which silently
		// drops the entire phase handoff. Opt back in explicitly.
		buf.WriteString("          include-hidden-files: true\n")
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
		// $PWD, NOT ${{ github.workspace }}: inside a container job the github.workspace
		// EXPRESSION resolves to the host path (/home/runner/work/<repo>/<repo>), which
		// does not exist inside the container — only the bind-mount /__w/<repo>/<repo>
		// does. $PWD is the container's actual cwd, so the cistate audition writes, the
		// artifact perform extracts, and assertAuditionRan all agree on one real path.
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

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
