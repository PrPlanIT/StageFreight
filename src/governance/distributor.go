package governance

import (
	"bytes"
	"fmt"
)

// PlanDistribution computes what files need to change for each governed repo.
// Pure planning — does NOT write anything.
// Reads current state from forge to detect drift and determine actions.
func PlanDistribution(
	gov *GovernanceConfig,
	presetLoader PresetLoader,
	skeleton []byte,
	auxFiles map[string][]byte, // e.g., ".claude/settings.json" → content
	forgeReader ForgeReader,
	sourceIdentity string, // e.g., "PrPlanIT/MaintenancePolicy"
	sourceRef string,
) ([]DistributionPlan, error) {

	var plans []DistributionPlan

	for _, cluster := range gov.Clusters {
		// Resolve presets in the cluster's stagefreight config.
		resolvedConfig, _, err := ResolvePresets(
			cluster.Config, presetLoader,
			sourceIdentity+"@"+sourceRef,
			".stagefreight.yml",
			0, nil,
		)
		if err != nil {
			return nil, fmt.Errorf("cluster %q: resolving presets: %w", cluster.ID, err)
		}

		// Render sealed .stagefreight.yml.
		seal := SealMeta{
			SourceRepo: sourceIdentity,
			SourceRef:  sourceRef,
			ClusterID:  cluster.ID,
		}

		sealedContent, err := RenderSealedConfig(seal, resolvedConfig)
		if err != nil {
			return nil, fmt.Errorf("cluster %q: rendering sealed config: %w", cluster.ID, err)
		}

		for _, repo := range cluster.Targets.Repos {
			plan := DistributionPlan{Repo: repo}

			// Sealed .stagefreight.yml — the repo's actual config.
			plan.Files = append(plan.Files, planFile(
				forgeReader, repo,
				".stagefreight.yml",
				sealedContent,
			))

			// CI skeleton (if configured).
			if len(skeleton) > 0 {
				plan.Files = append(plan.Files, planFile(
					forgeReader, repo,
					".gitlab-ci.yml",
					skeleton,
				))
			}

			// Auxiliary files (claude-code settings, precommit, etc.).
			for path, content := range auxFiles {
				plan.Files = append(plan.Files, planFile(
					forgeReader, repo,
					path,
					content,
				))
			}

			plans = append(plans, plan)
		}
	}

	return plans, nil
}

// ForgeReader reads current file content from a remote repo.
// Used to detect drift and determine create vs update actions.
type ForgeReader interface {
	GetFileContent(repo, path, ref string) ([]byte, error)
}

// planFile determines the action for a single file in a target repo.
func planFile(reader ForgeReader, repo, path string, newContent []byte) DistributedFile {
	f := DistributedFile{
		Path:    path,
		Content: newContent,
	}

	if reader == nil {
		// No reader available — assume create.
		f.Action = "create"
		return f
	}

	existing, err := reader.GetFileContent(repo, path, "HEAD")
	if err != nil {
		// File doesn't exist — create.
		f.Action = "create"
		return f
	}

	if bytes.Equal(existing, newContent) {
		f.Action = "unchanged"
		return f
	}

	// File exists but differs — governance-governed files are authoritative.
	// Any difference on a governed file is drift that gets replaced.
	f.Action = "replace-drifted"
	f.Drifted = true

	return f
}

// HasChanges returns true if this plan has any files that need writing.
func (p DistributionPlan) HasChanges() bool {
	for _, f := range p.Files {
		if f.Action != "unchanged" {
			return true
		}
	}
	return false
}
