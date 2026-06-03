// Package azurepipelines is a private serialization backend: it writes a
// forge-neutral model.Pipeline out as an Azure DevOps pipeline (azure-pipelines.yml).
//
// Mechanism, not identity. It lives under ci/render/internal so it is importable
// only by the render layer and never appears in a user-facing surface. It shares
// nothing with the Actions backend — Azure's stage/job/step model and YAML are
// its own — so it is a separate backend rather than a dialect of another.
//
// Lowering decisions:
//   - stages → Azure has first-class stages; each model job maps to a stage
//     (one job inside) and stage ordering is expressed with dependsOn. A job with
//     no explicit Needs depends on the preceding stage; first-stage jobs depend on
//     nothing.
//   - artifacts → PublishPipelineArtifact (publish) on the producer; a consumer
//     that needs it uses DownloadPipelineArtifact (download).
//   - allow-failure → job continueOnError; when-always → stage condition: always().
//   - container → the CI image is declared as a container resource and each job
//     runs in it.
//
// First-pass runtime caveats to validate against a real Azure DevOps run (the
// structure is stable; these are environment specifics, like the Actions backend's
// DinD/OIDC caveats):
//   - OIDC: Azure issues federated tokens via service connections, not a generic
//     audience request. The STAGEFREIGHT_OIDC contract needs a service-connection
//     -backed token step; this backend emits a marked placeholder, not a working
//     fetch.
//   - docker: Azure's hosted agents have Docker on the host; a container job needs
//     the host socket or a self-hosted agent. The transport env is emitted but the
//     daemon wiring is environment-specific.
//   - artifact path: download restores under $(Pipeline.Workspace)/<name>; mapping
//     it back onto the workspace .stagefreight/ tree is a refinement.
package azurepipelines

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/ci/render/model"
)

// Dialect carries the per-call values the azuredevops emitter supplies. Only
// genuinely varying values belong here; the backend chooses nothing about identity.
type Dialect struct {
	// Provider is the forge identity string, written into the header and SF_CI_PROVIDER.
	Provider string
}

// Emit serializes a forge-neutral Pipeline to azure-pipelines.yml bytes. Pure and
// deterministic: identical (p, d) → identical bytes.
func Emit(p model.Pipeline, d Dialect) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(header(d.Provider))

	// ── triggers ─────────────────────────────────────────────────────────────
	buf.WriteString("\ntrigger:\n  branches:\n    include:\n      - '*'\n")
	buf.WriteString("\npr:\n  branches:\n    include:\n      - '*'\n")

	// ── agent pool ───────────────────────────────────────────────────────────
	buf.WriteString("\npool:\n  vmImage: ubuntu-latest\n")

	// ── container resource (the CI image) ────────────────────────────────────
	if p.Defaults.Image != "" {
		buf.WriteString("\nresources:\n  containers:\n    - container: ci\n")
		fmt.Fprintf(&buf, "      image: %s\n", p.Defaults.Image)
	}

	stages := orderStages(p)
	producers := map[string]bool{}
	for _, j := range p.Jobs {
		if len(j.Artifacts.Paths) > 0 {
			producers[j.Name] = true
		}
	}

	buf.WriteString("\nstages:\n")
	for _, j := range p.Jobs {
		emitStage(&buf, j, p.Defaults, d.Provider, stages, producers)
	}

	return buf.Bytes(), nil
}

func emitStage(buf *bytes.Buffer, j model.Job, def model.PipelineDefaults, provider string, stages stageOrder, producers map[string]bool) {
	needs := effectiveNeeds(j, stages)

	fmt.Fprintf(buf, "  - stage: %s\n", j.Stage)
	if len(needs) > 0 {
		fmt.Fprintf(buf, "    dependsOn: [%s]\n", strings.Join(needs, ", "))
	} else {
		buf.WriteString("    dependsOn: []\n")
	}
	// run regardless of upstream outcome (GitLab when: always)
	if j.Policy.WhenAlways {
		buf.WriteString("    condition: always()\n")
	}

	buf.WriteString("    jobs:\n")
	fmt.Fprintf(buf, "      - job: %s\n", j.Name)
	if def.Image != "" {
		buf.WriteString("        container: ci\n")
	}
	if j.Policy.AllowFailure {
		buf.WriteString("        continueOnError: true\n")
	}
	// routing labels → self-hosted pool demands
	if len(j.Routing.Labels) > 0 {
		buf.WriteString("        pool:\n")
		buf.WriteString("          demands:\n")
		for _, l := range j.Routing.Labels {
			fmt.Fprintf(buf, "            - %s\n", l)
		}
	}
	// docker transport env (caveat: daemon wiring is agent-specific)
	if j.Capabilities.Docker {
		buf.WriteString("        variables:\n")
		buf.WriteString("          DOCKER_HOST: tcp://localhost:2375\n")
	}

	buf.WriteString("        steps:\n")

	// checkout (full clone when requested)
	buf.WriteString("          - checkout: self\n")
	if j.Source.FullClone {
		buf.WriteString("            fetchDepth: 0\n")
	}

	// OIDC placeholder — Azure federated identity is service-connection-backed;
	// this is not a working token fetch (see package caveats).
	if j.Capabilities.OIDC {
		buf.WriteString("          # OIDC: wire STAGEFREIGHT_OIDC via an Azure service connection / workload identity federation.\n")
	}

	// download artifacts from each upstream producer this job depends on
	for _, n := range needs {
		if producers[n] {
			buf.WriteString("          - download: current\n")
			fmt.Fprintf(buf, "            artifact: %s\n", n)
		}
	}

	// the phase command(s), with SF_CI context exported inline (Azure script steps
	// do not share env unless promoted, so the context is set in the same shell).
	buf.WriteString("          - script: |\n")
	if def.CIContext {
		for _, line := range ciContextExports(provider) {
			fmt.Fprintf(buf, "              %s\n", line)
		}
	}
	for _, cmd := range j.Commands {
		fmt.Fprintf(buf, "              %s\n", cmd)
	}
	fmt.Fprintf(buf, "            displayName: %s\n", j.Name)

	// publish this job's artifacts for downstream consumers
	for idx, pth := range j.Artifacts.Paths {
		name := j.Name
		if len(j.Artifacts.Paths) > 1 {
			name = fmt.Sprintf("%s-%d", j.Name, idx)
		}
		fmt.Fprintf(buf, "          - publish: %s\n", pth)
		fmt.Fprintf(buf, "            artifact: %s\n", name)
		if j.Policy.WhenAlways {
			buf.WriteString("            condition: always()\n")
		}
	}
}

// header is the provider-stamped banner — same shape as every StageFreight
// skeleton header.
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

// ciContextExports maps Azure predefined variables to the SF_CI_* contract.
// SF_CI_PROVIDER carries the calling forge.
func ciContextExports(provider string) []string {
	return []string{
		fmt.Sprintf(`export SF_CI_PROVIDER=%s`, provider),
		`export SF_CI_EVENT="$(Build.Reason)"`,
		`export SF_CI_BRANCH="$(Build.SourceBranchName)"`,
		`export SF_CI_SHA="$(Build.SourceVersion)"`,
		`export SF_CI_REPO_URL="$(Build.Repository.Uri)"`,
		`export SF_CI_PIPELINE_ID="$(Build.BuildId)"`,
		`export SF_CI_WORKSPACE="$(Build.SourcesDirectory)"`,
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
// dependsOn DAG. First-stage jobs get no dependencies.
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
