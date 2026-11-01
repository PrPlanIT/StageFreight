package config

import (
	"fmt"
	"strings"
)

// Secrets declares secret brokers — backends that issue short-lived credentials to a
// CI job authenticated by the job's OWN OIDC identity. A broker is the shared auth
// kernel: KMS signing consumes a session today (signing.profiles[].kms.broker), and
// job secret composition will consume the same kernel later. It is deliberately
// backend-agnostic and carries NO deployment specifics — the address, auth mount, and
// roles bind at runtime via SF_SECRETS_<ID>_* (the same discipline as SF_KMS_<REF>),
// so a profile stays portable and core never learns a vault URL, mount, or role string.
// See the design note: stagefreight-scratchpad/design/secret-broker.md.
type SecretsConfig struct {
	// Brokers is the set of declared brokers, referenced by id from a kms profile's
	// broker field. A sequence (not a map) — order is immaterial and ids are unique.
	Brokers []BrokerConfig `yaml:"brokers,omitempty"`
}

// BrokerConfig is one named broker: a logical id, the protocol family that selects a
// backend, and how a job authenticates to it. Nothing here names an address, a role,
// or a mount — those are runtime-bound.
type BrokerConfig struct {
	ID   string     `yaml:"id"`             // logical ref; → SF_SECRETS_<ID>_* at runtime
	Kind string     `yaml:"kind"`           // protocol family: "vault" (OpenBao speaks vault)
	Auth BrokerAuth `yaml:"auth,omitempty"` // how a job proves identity (default: oidc)
}

// BrokerAuth is how a job authenticates to a broker.
type BrokerAuth struct {
	// Method is "oidc" (present the job's forge OIDC id_token — the default) or "token"
	// (the operator supplies ambient credentials, e.g. VAULT_TOKEN already in env).
	Method string `yaml:"method,omitempty"`
	// Audience is the OIDC aud the broker's role binds and the CI job mints. Empty
	// defaults to "stagefreight", reusing the id_token the signing job already emits.
	// Consumed by the CI emitter (to mint a non-default audience), not by the login.
	Audience string `yaml:"audience,omitempty"`
}

// Broker kinds and auth methods — the only valid values.
const (
	BrokerKindVault = "vault" // HashiCorp Vault / OpenBao

	BrokerAuthOIDC  = "oidc"
	BrokerAuthToken = "token"
)

var validBrokerKinds = map[string]bool{BrokerKindVault: true}
var validBrokerAuthMethods = map[string]bool{BrokerAuthOIDC: true, BrokerAuthToken: true}

// DefaultBrokerAudience is the OIDC audience a broker login uses when none is declared
// — the same audience the perform job already mints (STAGEFREIGHT_OIDC).
const DefaultBrokerAudience = "stagefreight"

// FindBrokerByID returns the broker with this id, or nil.
func FindBrokerByID(brokers []BrokerConfig, id string) *BrokerConfig {
	for i := range brokers {
		if brokers[i].ID == id {
			return &brokers[i]
		}
	}
	return nil
}

// ValidateSecretsConfig validates the broker declarations: non-empty unique ids, a
// known kind, and a known auth method (empty = oidc default). It is the single
// validation layer for the secrets block.
func ValidateSecretsConfig(s SecretsConfig) []string {
	var errs []string
	seen := map[string]bool{}
	for i, b := range s.Brokers {
		where := fmt.Sprintf("secrets.brokers[%d]", i)
		if strings.TrimSpace(b.ID) == "" {
			errs = append(errs, where+": an entry has an empty id")
		} else {
			where = fmt.Sprintf("secrets.brokers[%s]", b.ID)
			if seen[b.ID] {
				errs = append(errs, where+": duplicate id")
			}
			seen[b.ID] = true
		}
		if !validBrokerKinds[b.Kind] {
			errs = append(errs, fmt.Sprintf("%s: unknown kind %q (known: %s)", where, b.Kind, BrokerKindVault))
		}
		if b.Auth.Method != "" && !validBrokerAuthMethods[b.Auth.Method] {
			errs = append(errs, fmt.Sprintf("%s: unknown auth.method %q (expected %q or %q)", where, b.Auth.Method, BrokerAuthOIDC, BrokerAuthToken))
		}
	}
	return errs
}
