package docker

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DetectDrift computes drift state for a stack against stored hash stamps.
// Tier 1: bundle hash comparison. Tier 2 (container config hash) is deferred
// until transport is wired for remote Docker API queries.
func DetectDrift(stack StackInfo, rootDir string, stamps *HashStamps, secrets SecretsProvider) DriftResult {
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

	return DriftResult{
		Stack:      key,
		Drifted:    false,
		Reason:     "no drift detected",
		BundleHash: currentHash,
		StoredHash: stored.BundleHash,
	}
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
