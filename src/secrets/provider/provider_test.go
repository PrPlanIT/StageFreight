package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

func vaultProvider() []config.ProviderConfig {
	return []config.ProviderConfig{{ID: "openbao", Kind: config.ProviderKindVault, Auth: config.ProviderAuth{Method: config.ProviderAuthOIDC}}}
}

// cleanVaultEnv forces a clean, auto-restored VAULT_* so EnsureSession's os.Setenv
// never leaks across tests (t.Setenv restores the pre-test value on cleanup).
func cleanVaultEnv(t *testing.T) {
	t.Helper()
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("VAULT_ADDR", "")
}

// fakeVault returns a server that accepts a JWT login at the given mount path and
// issues token; it records the last login path and role it saw.
func fakeVault(t *testing.T, token string) (*httptest.Server, *struct {
	Path string
	Role string
	JWT  string
}) {
	t.Helper()
	seen := &struct {
		Path string
		Role string
		JWT  string
	}{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		var in struct{ Role, JWT string }
		_ = json.Unmarshal(body, &in)
		seen.Role, seen.JWT = in.Role, in.JWT
		if !strings.HasSuffix(r.URL.Path, "/login") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"auth":{"client_token":"`+token+`"}}`)
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

func TestEnsureSession_NoProviderRef_NoOp(t *testing.T) {
	cleanVaultEnv(t)
	if err := EnsureSession(context.Background(), vaultProvider(), "", ""); err != nil {
		t.Fatalf("empty provider ref must be a no-op, got %v", err)
	}
	if os.Getenv("VAULT_TOKEN") != "" {
		t.Error("no-op must not set VAULT_TOKEN")
	}
}

func TestEnsureSession_UnknownProvider_Errors(t *testing.T) {
	cleanVaultEnv(t)
	err := EnsureSession(context.Background(), vaultProvider(), "nope", "r")
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("want a 'not declared' error, got %v", err)
	}
}

func TestEnsureVaultSession_HappyPath(t *testing.T) {
	cleanVaultEnv(t)
	srv, seen := fakeVault(t, "s.good")
	t.Setenv("SF_SECRETS_OPENBAO_ADDR", srv.URL)
	t.Setenv("SF_SECRETS_OPENBAO_LOGIN", "gitlab-ci")
	t.Setenv("SF_SECRETS_OPENBAO_ROLE_COSIGN_HOMELABHD", "stagefreight-cosign-homelabhd")
	t.Setenv("STAGEFREIGHT_OIDC", "the-id-token")

	if err := EnsureSession(context.Background(), vaultProvider(), "openbao", "cosign-homelabhd"); err != nil {
		t.Fatalf("login should succeed: %v", err)
	}
	if got := os.Getenv("VAULT_TOKEN"); got != "s.good" {
		t.Errorf("VAULT_TOKEN = %q, want s.good", got)
	}
	if got := os.Getenv("VAULT_ADDR"); got != srv.URL {
		t.Errorf("VAULT_ADDR = %q, want %q", got, srv.URL)
	}
	if seen.Path != "/v1/auth/gitlab-ci/login" {
		t.Errorf("login path = %q, want /v1/auth/gitlab-ci/login", seen.Path)
	}
	if seen.Role != "stagefreight-cosign-homelabhd" {
		t.Errorf("login role = %q, want the resolved concrete role", seen.Role)
	}
	if seen.JWT != "the-id-token" {
		t.Errorf("login jwt = %q, want the STAGEFREIGHT_OIDC id_token", seen.JWT)
	}
}

func TestEnsureVaultSession_DefaultLoginPath(t *testing.T) {
	cleanVaultEnv(t)
	srv, seen := fakeVault(t, "s.x")
	t.Setenv("SF_SECRETS_OPENBAO_ADDR", srv.URL)
	// no SF_SECRETS_OPENBAO_LOGIN → defaults to "jwt"
	t.Setenv("SF_SECRETS_OPENBAO_ROLE_R", "role-r")
	t.Setenv("STAGEFREIGHT_OIDC", "tok")
	if err := EnsureSession(context.Background(), vaultProvider(), "openbao", "r"); err != nil {
		t.Fatalf("login should succeed: %v", err)
	}
	if seen.Path != "/v1/auth/jwt/login" {
		t.Errorf("login path = %q, want the jwt default", seen.Path)
	}
}

func TestEnsureVaultSession_Idempotent(t *testing.T) {
	cleanVaultEnv(t)
	t.Setenv("VAULT_TOKEN", "already") // a live session / ambient token
	// Deliberately set NO address: if the function tried to log in it would error;
	// idempotency must short-circuit before any env lookup or network call.
	if err := EnsureSession(context.Background(), vaultProvider(), "openbao", "r"); err != nil {
		t.Fatalf("an existing VAULT_TOKEN must short-circuit, got %v", err)
	}
	if os.Getenv("VAULT_TOKEN") != "already" {
		t.Error("existing token must be left untouched")
	}
}

func TestEnsureVaultSession_MissingAddr_Errors(t *testing.T) {
	cleanVaultEnv(t)
	t.Setenv("STAGEFREIGHT_OIDC", "tok")
	t.Setenv("SF_SECRETS_OPENBAO_ROLE_R", "role-r")
	err := EnsureSession(context.Background(), vaultProvider(), "openbao", "r")
	if err == nil || !strings.Contains(err.Error(), "_ADDR") {
		t.Fatalf("missing address must fail loud, got %v", err)
	}
}

func TestEnsureVaultSession_MissingRole_Errors(t *testing.T) {
	cleanVaultEnv(t)
	t.Setenv("SF_SECRETS_OPENBAO_ADDR", "http://vault.invalid")
	t.Setenv("STAGEFREIGHT_OIDC", "tok")
	err := EnsureSession(context.Background(), vaultProvider(), "openbao", "r")
	if err == nil || !strings.Contains(err.Error(), "ROLE_R") {
		t.Fatalf("missing role var must fail loud, got %v", err)
	}
}

func TestEnsureVaultSession_MissingIDToken_Errors(t *testing.T) {
	cleanVaultEnv(t)
	t.Setenv("SF_SECRETS_OPENBAO_ADDR", "http://vault.invalid")
	t.Setenv("SF_SECRETS_OPENBAO_ROLE_R", "role-r")
	t.Setenv("STAGEFREIGHT_OIDC", "")
	err := EnsureSession(context.Background(), vaultProvider(), "openbao", "r")
	if err == nil || !strings.Contains(err.Error(), "STAGEFREIGHT_OIDC") {
		t.Fatalf("missing id_token must fail loud, got %v", err)
	}
}

func TestEnsureVaultSession_TokenAuthWithoutToken_Errors(t *testing.T) {
	cleanVaultEnv(t)
	providers := []config.ProviderConfig{{ID: "openbao", Kind: config.ProviderKindVault, Auth: config.ProviderAuth{Method: config.ProviderAuthToken}}}
	t.Setenv("SF_SECRETS_OPENBAO_ADDR", "http://vault.invalid")
	err := EnsureSession(context.Background(), providers, "openbao", "r")
	if err == nil || !strings.Contains(err.Error(), config.ProviderAuthToken) {
		t.Fatalf("auth:token with no ambient VAULT_TOKEN must fail loud, got %v", err)
	}
}

func TestEnsureVaultSession_LoginNoToken_Errors(t *testing.T) {
	cleanVaultEnv(t)
	srv, _ := fakeVault(t, "") // responds 200 but with an empty client_token
	t.Setenv("SF_SECRETS_OPENBAO_ADDR", srv.URL)
	t.Setenv("SF_SECRETS_OPENBAO_ROLE_R", "role-r")
	t.Setenv("STAGEFREIGHT_OIDC", "tok")
	err := EnsureSession(context.Background(), vaultProvider(), "openbao", "r")
	if err == nil || !strings.Contains(err.Error(), "no client token") {
		t.Fatalf("an empty client token must fail loud, got %v", err)
	}
}

func TestEnsureVaultSession_LoginHTTPError_Errors(t *testing.T) {
	cleanVaultEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"errors":["permission denied"]}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SF_SECRETS_OPENBAO_ADDR", srv.URL)
	t.Setenv("SF_SECRETS_OPENBAO_ROLE_R", "role-r")
	t.Setenv("STAGEFREIGHT_OIDC", "tok")
	err := EnsureSession(context.Background(), vaultProvider(), "openbao", "r")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("a non-200 login must fail loud with the status, got %v", err)
	}
}
