package docker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// SOPSProvider implements SecretsProvider using the sops CLI.
type SOPSProvider struct{}

func (s *SOPSProvider) Name() string { return "sops" }

// Decrypt decrypts a SOPS-encrypted file and returns the plaintext.
func (s *SOPSProvider) Decrypt(_ context.Context, path string) ([]byte, error) {
	cmd := exec.Command("sops", "--decrypt", path)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("sops decrypt %s: %s", path, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("sops decrypt %s: %w", path, err)
	}
	return out, nil
}

// Encrypt encrypts data and writes it to path using SOPS.
func (s *SOPSProvider) Encrypt(_ context.Context, path string, data []byte) error {
	// SOPS encrypts in-place, so we write the plaintext first then encrypt
	cmd := exec.Command("sops", "--encrypt", "--in-place", path)
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sops encrypt %s: %s", path, strings.TrimSpace(string(out)))
	}
	return nil
}

// IsEncrypted checks if a file path matches SOPS naming conventions.
func (s *SOPSProvider) IsEncrypted(path string) bool {
	return strings.Contains(path, "_secret") || strings.Contains(path, "_private") ||
		strings.HasSuffix(path, ".enc.yaml") || strings.HasSuffix(path, ".enc.yml") ||
		strings.HasSuffix(path, ".enc.json")
}

// ResolveSecretsProvider returns the appropriate provider by name.
func ResolveSecretsProvider(name string) (SecretsProvider, error) {
	switch name {
	case "sops", "":
		if _, err := exec.LookPath("sops"); err != nil {
			return nil, fmt.Errorf("sops binary not found in PATH")
		}
		return &SOPSProvider{}, nil
	case "vault":
		return nil, fmt.Errorf("secrets provider 'vault' not implemented")
	case "infisical":
		return nil, fmt.Errorf("secrets provider 'infisical' not implemented")
	default:
		return nil, fmt.Errorf("unknown secrets provider: %q", name)
	}
}
