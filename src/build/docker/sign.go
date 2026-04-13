package docker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// CosignSign signs an image digest ref using cosign.
// The digestRef must be in the form repo@sha256:... — tags are never used.
func CosignSign(ctx context.Context, rootDir string, desired map[string]config.ToolPinConfig, digestRef, keyPath string, multiArch bool) error {
	ver, pinned := toolchain.ResolveVersion("cosign", "", desired)
	result, err := toolchain.Resolve(rootDir, "cosign", ver)
	if err != nil {
		if pinned {
			return fmt.Errorf("cosign sign: pinned version %s failed to resolve: %w", ver, err)
		}
		return fmt.Errorf("cosign sign: toolchain resolve: %w", err)
	}

	args := []string{"sign",
		"--key", keyPath,
		"--tlog-upload=false",
		"--upload=true",
	}
	if multiArch {
		args = append(args, "--recursive")
	}
	args = append(args, digestRef)

	cmd := exec.CommandContext(ctx, result.Path, args...)
	cmd.Env = append(toolchain.CleanEnv(), "COSIGN_YES=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		diag.Warn("cosign sign failed: %s", strings.TrimSpace(string(out)))
		return fmt.Errorf("cosign sign: %w", err)
	}
	return nil
}

// CosignAttest attests a predicate against an image digest ref using cosign.
// The digestRef must be in the form repo@sha256:... — tags are never used.
func CosignAttest(ctx context.Context, rootDir string, desired map[string]config.ToolPinConfig, digestRef, predicatePath, keyPath string) error {
	ver, pinned := toolchain.ResolveVersion("cosign", "", desired)
	result, err := toolchain.Resolve(rootDir, "cosign", ver)
	if err != nil {
		if pinned {
			return fmt.Errorf("cosign attest: pinned version %s failed to resolve: %w", ver, err)
		}
		return fmt.Errorf("cosign attest: toolchain resolve: %w", err)
	}

	cmd := exec.CommandContext(ctx, result.Path, "attest",
		"--key", keyPath,
		"--predicate", predicatePath,
		"--type", "slsaprovenance",
		"--tlog-upload=false",
		"--upload=true",
		digestRef)
	cmd.Env = append(toolchain.CleanEnv(), "COSIGN_YES=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		diag.Warn("cosign attest failed: %s", strings.TrimSpace(string(out)))
		return fmt.Errorf("cosign attest: %w", err)
	}
	return nil
}

// ResolveCosignKey finds the cosign signing key path.
// Checks COSIGN_KEY env var first, then .stagefreight/cosign.key.
func ResolveCosignKey() string {
	if key := os.Getenv("COSIGN_KEY"); key != "" {
		return key
	}
	keyPath := ".stagefreight/cosign.key"
	if _, err := os.Stat(keyPath); err == nil {
		return keyPath
	}
	return ""
}

// CosignAvailable returns true if cosign can be resolved via toolchain.
func CosignAvailable(rootDir string, desired map[string]config.ToolPinConfig) bool {
	ver, _ := toolchain.ResolveVersion("cosign", "", desired)
	_, err := toolchain.Resolve(rootDir, "cosign", ver)
	return err == nil
}
