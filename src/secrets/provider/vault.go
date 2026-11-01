package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// defaultVaultLoginPath is Vault's conventional JWT auth mount. Operators point
// SF_SECRETS_<REF>_LOGIN at their actual path (e.g. "gitlab-ci"); this is the fallback
// so the common case needs one fewer variable.
const defaultVaultLoginPath = "jwt"

// vaultLoginTimeout bounds the login round-trip; a hung provider must not stall the job.
const vaultLoginTimeout = 30 * time.Second

// ensureVaultSession logs into Vault/OpenBao over its JWT auth method, presenting the
// job's OIDC id_token (STAGEFREIGHT_OIDC), and installs VAULT_ADDR + VAULT_TOKEN into
// the process environment. cosign's hashivault:// provider (and the vault CLI) read
// exactly those two vars, so the existing kms env-forwarding then reaches the key with
// no change.
//
// Idempotent: if VAULT_TOKEN is already set it returns immediately — covering both a
// prior call in the same run and the auth: token mode where the operator supplies the
// token ambiently. Every failure is loud: a missing binding or a login that yields no
// token is an error, never a silent empty session.
func ensureVaultSession(ctx context.Context, b config.ProviderConfig, roleRef string) error {
	if os.Getenv("VAULT_TOKEN") != "" {
		return nil // session already established / ambient token supplied
	}

	addr := strings.TrimRight(os.Getenv(envName(b.ID, "ADDR")), "/")
	if addr == "" {
		return fmt.Errorf("secret provider %q: %s is not set (the Vault/OpenBao address)", b.ID, envName(b.ID, "ADDR"))
	}

	// auth: token means the operator supplies VAULT_TOKEN ambiently. Reaching here with
	// none set (the check above) is a misconfiguration — surface it, do not sign tokenless.
	if b.Auth.Method == config.ProviderAuthToken {
		return fmt.Errorf("secret provider %q: auth.method is %q but VAULT_TOKEN is not present in the environment", b.ID, config.ProviderAuthToken)
	}

	idToken := os.Getenv("STAGEFREIGHT_OIDC")
	if idToken == "" {
		return fmt.Errorf("secret provider %q: no OIDC id_token in the environment (STAGEFREIGHT_OIDC) — the signing job must mint one", b.ID)
	}

	roleVar := envName(b.ID, "ROLE_"+normRef(roleRef))
	role := os.Getenv(roleVar)
	if role == "" {
		return fmt.Errorf("secret provider %q: %s is not set (the Vault role for ref %q)", b.ID, roleVar, roleRef)
	}

	loginPath := os.Getenv(envName(b.ID, "LOGIN"))
	if loginPath == "" {
		loginPath = defaultVaultLoginPath
	}

	token, err := vaultJWTLogin(ctx, addr, loginPath, role, idToken)
	if err != nil {
		return fmt.Errorf("secret provider %q: Vault login failed: %w", b.ID, err)
	}
	if token == "" {
		return fmt.Errorf("secret provider %q: Vault login returned no client token", b.ID)
	}

	// Install the session for the rest of the process. The existing signEnv forwards
	// VAULT_* into cosign; nothing in the signing core learns about this package.
	if err := os.Setenv("VAULT_ADDR", addr); err != nil {
		return fmt.Errorf("secret provider %q: setting VAULT_ADDR: %w", b.ID, err)
	}
	if err := os.Setenv("VAULT_TOKEN", token); err != nil {
		return fmt.Errorf("secret provider %q: setting VAULT_TOKEN: %w", b.ID, err)
	}
	return nil
}

// vaultJWTLogin POSTs {role, jwt} to <addr>/v1/auth/<path>/login and returns the issued
// client token. Pure transport: no global state, the caller installs the result. TLS
// uses the system trust store (an internal http:// address or a publicly-trusted https
// address both work); a custom CA / VAULT_CACERT is a documented future refinement.
func vaultJWTLogin(ctx context.Context, addr, loginPath, role, jwt string) (string, error) {
	reqBody, err := json.Marshal(map[string]string{"role": role, "jwt": jwt})
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/v1/auth/%s/login", addr, strings.Trim(loginPath, "/"))

	reqCtx, cancel := context.WithTimeout(ctx, vaultLoginTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var out struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decoding %s response: %w", url, err)
	}
	return out.Auth.ClientToken, nil
}
