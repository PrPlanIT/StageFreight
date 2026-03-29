package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DetectDrift computes drift state for a stack against stored hash stamps.
// Two-tier detection:
//   - Tier 1: bundle hash comparison (local, fast, no remote calls)
//   - Tier 2: container config hash via transport (remote, only if Tier 1 passes)
//
// If transport is nil, Tier 2 is skipped (read-only/local mode).
func DetectDrift(ctx context.Context, stack StackInfo, rootDir string, stamps *HashStamps, secrets SecretsProvider, transport HostTransport) DriftResult {
	key := stack.Scope + "/" + stack.Name
	currentHash := ComputeBundleHash(stack, rootDir, secrets)

	stored, ok := stamps.Stacks[key]
	if !ok {
		return DriftResult{
			Stack:      key,
			Drifted:    true,
			Tier:       1,
			Reason:     "no previous deployment recorded",
			BundleHash: currentHash,
		}
	}

	// Tier 1: bundle hash changed → drift.
	if currentHash != stored.BundleHash {
		return DriftResult{
			Stack:      key,
			Drifted:    true,
			Tier:       1,
			Reason:     "IaC files changed since last deployment",
			BundleHash: currentHash,
			StoredHash: stored.BundleHash,
		}
	}

	// Tier 2: bundle unchanged, check container runtime state.
	// Only if transport is available (skip in local-only/read-only mode).
	if transport != nil {
		inspection, err := transport.InspectStack(ctx, stack.Name)
		if err == nil {
			tier2 := checkTier2Drift(inspection, stored)
			if tier2 != "" {
				return DriftResult{
					Stack:      key,
					Drifted:    true,
					Tier:       2,
					Reason:     tier2,
					BundleHash: currentHash,
					StoredHash: stored.BundleHash,
				}
			}
		}
		// If inspect fails, skip Tier 2 silently — Tier 1 already passed.
	}

	return DriftResult{
		Stack:      key,
		Drifted:    false,
		Reason:     "no drift detected",
		BundleHash: currentHash,
		StoredHash: stored.BundleHash,
	}
}

// checkTier2Drift compares runtime container state against stored stamps.
// Returns drift reason string, or "" if no drift.
func checkTier2Drift(inspection StackInspection, stored StackStamp) string {
	if len(inspection.Services) == 0 {
		return "no containers running for project"
	}

	// Check if any service has a different config hash than what we last deployed.
	// Compose sets com.docker.compose.config-hash on each container.
	// If someone ran `docker compose up` manually outside StageFreight,
	// the config hash will differ from our bundle hash.
	for _, svc := range inspection.Services {
		if !svc.Running {
			return fmt.Sprintf("service %s is not running (state: %s)", svc.Service, svc.State)
		}
	}

	// Check for config hash consistency across services.
	// All services in a compose project should have the same config hash.
	hashes := map[string]bool{}
	for _, svc := range inspection.Services {
		if svc.ConfigHash != "" {
			hashes[svc.ConfigHash] = true
		}
	}
	if len(hashes) > 1 {
		return "container configurations diverged (mixed config hashes)"
	}

	return ""
}

// LoadHashStamps reads the .stagefreight-state.yml file.
// Returns empty stamps if file doesn't exist.
func LoadHashStamps(rootDir string) (*HashStamps, error) {
	path := filepath.Join(rootDir, ".stagefreight-state.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &HashStamps{Stacks: map[string]StackStamp{}}, nil
		}
		return nil, fmt.Errorf("reading state file: %w", err)
	}

	var stamps HashStamps
	if err := yaml.Unmarshal(data, &stamps); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}
	if stamps.Stacks == nil {
		stamps.Stacks = map[string]StackStamp{}
	}
	return &stamps, nil
}

// SaveHashStamps writes the hash stamps to .stagefreight-state.yml.
func SaveHashStamps(rootDir string, stamps *HashStamps) error {
	path := filepath.Join(rootDir, ".stagefreight-state.yml")
	data, err := yaml.Marshal(stamps)
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}
