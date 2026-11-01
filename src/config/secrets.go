package config

import (
	"fmt"
	"strings"
)

// Secrets declares secret providers — backends that issue short-lived credentials to a
// CI job authenticated by the job's OWN OIDC identity. A provider is the shared auth
// kernel: KMS signing consumes a session today (signing.profiles[].kms.provider), and
// job secret composition will consume the same kernel later (secrets.reads, alongside
// providers). It is deliberately backend-agnostic and carries NO deployment specifics —
// the address, auth mount, and roles bind at runtime via SF_SECRETS_<ID>_* (the same
// discipline as SF_KMS_<REF>), so a profile stays portable and core never learns a vault
// URL, mount, or role string. See the design note:
// stagefreight-scratchpad/design/secret-provider.md.
type SecretsConfig struct {
	// Providers is the set of declared providers, referenced by id from a kms profile's
	// provider field. Authored as an id → provider map (the id is the key), the same
	// shape as every other collection in the schema.
	Providers OrderedProviders `yaml:"providers,omitempty"`
}

// ProviderConfig is one named provider: a logical id (the map key), the protocol family
// that selects a backend, and how a job authenticates to it. Nothing here names an
// address, a role, or a mount — those are runtime-bound.
type ProviderConfig struct {
	ID   string       `yaml:"-"`              // from the map key; → SF_SECRETS_<ID>_* at runtime
	Kind string       `yaml:"kind"`           // protocol family: "vault" (OpenBao speaks vault)
	Auth ProviderAuth `yaml:"auth,omitempty"` // how a job proves identity (default: oidc)
}

// ProviderAuth is how a job authenticates to a provider.
type ProviderAuth struct {
	// Method is "oidc" (present the job's forge OIDC id_token — the default) or "token"
	// (the operator supplies ambient credentials, e.g. VAULT_TOKEN already in env).
	Method string `yaml:"method,omitempty"`
	// Audience is the OIDC aud the provider's role binds and the CI job mints. Empty
	// defaults to "stagefreight", reusing the id_token the signing job already emits.
	// Consumed by the CI emitter (to mint a non-default audience), not by the login.
	Audience string `yaml:"audience,omitempty"`
}

// Provider kinds and auth methods — the only valid values.
const (
	ProviderKindVault = "vault" // HashiCorp Vault / OpenBao

	ProviderAuthOIDC  = "oidc"
	ProviderAuthToken = "token"
)

var validProviderKinds = map[string]bool{ProviderKindVault: true}
var validProviderAuthMethods = map[string]bool{ProviderAuthOIDC: true, ProviderAuthToken: true}

// DefaultProviderAudience is the OIDC audience a provider login uses when none is declared
// — the same audience the perform job already mints (STAGEFREIGHT_OIDC).
const DefaultProviderAudience = "stagefreight"

// FindProviderByID returns the provider with this id, or nil.
func FindProviderByID(providers []ProviderConfig, id string) *ProviderConfig {
	for i := range providers {
		if providers[i].ID == id {
			return &providers[i]
		}
	}
	return nil
}

// ValidateSecretsConfig validates the provider declarations: a non-empty id (the map
// key), a known kind, and a known auth method (empty = oidc default). It is the single
// validation layer for the secrets block. Uniqueness is structural — the id is the map
// key, so duplicates cannot occur.
func ValidateSecretsConfig(s SecretsConfig) []string {
	var errs []string
	for _, p := range s.Providers {
		where := fmt.Sprintf("secrets.providers[%s]", p.ID)
		if strings.TrimSpace(p.ID) == "" {
			errs = append(errs, "secrets.providers: an entry has an empty id")
			where = "secrets.providers[]"
		}
		if !validProviderKinds[p.Kind] {
			errs = append(errs, fmt.Sprintf("%s: unknown kind %q (known: %s)", where, p.Kind, ProviderKindVault))
		}
		if p.Auth.Method != "" && !validProviderAuthMethods[p.Auth.Method] {
			errs = append(errs, fmt.Sprintf("%s: unknown auth.method %q (expected %q or %q)", where, p.Auth.Method, ProviderAuthOIDC, ProviderAuthToken))
		}
	}
	return errs
}
