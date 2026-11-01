// Package broker is StageFreight's secret-broker auth kernel. It logs a CI job into a
// backend (Vault/OpenBao today) using the job's OWN OIDC identity and installs the
// resulting short-lived credentials into the process environment — exactly as
// `vault login` would — so the existing signing path, which forwards VAULT_* from the
// environment into cosign, picks them up with no change.
//
// It is a SIBLING of the signing engine, never part of it: src/sign/cosign stays
// provider-agnostic (it only ever reads the environment); only this package and the
// SF_SECRETS_* env speak a provider's wire protocol. Signing consumes a session today;
// job secret composition will consume the SAME kernel later — one login, many
// consumers. See stagefreight-scratchpad/design/secret-broker.md.
//
// Provider specifics are isolated per Kind (one backend function each); adding
// kind: aws later adds one backend and touches nothing else.
package broker

import (
	"context"
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// EnsureSession establishes the broker session named by (brokerRef, roleRef) and
// installs its credentials into the process environment. It is a no-op when brokerRef
// is empty (no broker configured — the ambient-credential path) and is idempotent: a
// backend that already sees a live session (its token var set, e.g. from a prior call
// or operator-supplied ambient creds) does nothing.
//
// A required login that fails is a hard error. Callers in the signing path surface it
// unchanged, so with a profile's enforce on it becomes a blocked publish — never a
// silent unsigned ship.
func EnsureSession(ctx context.Context, brokers []config.BrokerConfig, brokerRef, roleRef string) error {
	if strings.TrimSpace(brokerRef) == "" {
		return nil // no broker named → ambient credential (pre-broker behaviour)
	}
	b := config.FindBrokerByID(brokers, brokerRef)
	if b == nil {
		// Defensive: ValidateSigningBrokerRefs rejects this at config load, so reaching
		// here means the two drifted — fail loud rather than sign with no session.
		return fmt.Errorf("secret broker %q is referenced but not declared under secrets.brokers", brokerRef)
	}
	switch b.Kind {
	case config.BrokerKindVault:
		return ensureVaultSession(ctx, *b, roleRef)
	default:
		return fmt.Errorf("secret broker %q: unknown kind %q (known: %s)", b.ID, b.Kind, config.BrokerKindVault)
	}
}

// envName builds the runtime binding variable for a broker: SF_SECRETS_<REF>_<SUFFIX>,
// with <REF> upper-cased and its - and . mapped to _ — the same discipline as
// SF_KMS_<REF>. The suffix is passed already in its final form (e.g. "ADDR",
// "ROLE_COSIGN_HOMELABHD").
func envName(ref, suffix string) string {
	up := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(ref))
	return "SF_SECRETS_" + up + "_" + suffix
}

// normRef upper-cases a ref and maps - and . to _, for use inside a var suffix.
func normRef(ref string) string {
	return strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(ref))
}
