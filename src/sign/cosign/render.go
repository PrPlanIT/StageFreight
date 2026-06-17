package cosign

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/sign"
)

// Render turns a trust contract (sign.SignPlan) into a concrete cosign invocation
// by SATISFYING its requirements against the declared Env. It is a capability-
// satisfaction emitter, not a mode selector — and pure given (plan, op, env): a
// constraint solver over distinct trust PRINCIPALS, then a flag emitter.
//
// The load-bearing rule is the principal count |D| for the plan's class:
//
//	|D| == 0 → error (no declared capability satisfies the contract)
//	|D| == 1 → use it
//	|D| >  1 → error (a genuine TRUST ambiguity — distinct keys could sign;
//	                  never silently pick one)
//
// One principal reachable many ways (the same hardware key via FIDO2 and PKCS#11)
// is |D| == 1, resolved by a deterministic internal transport preference — that is
// a transport choice, not a trust choice, so it is decided here, not refused.
//
// Render emits only the trust + policy flags (key/sk, tlog, upload). The op target
// — image digest, blob path, --predicate — is appended by the executor, which owns
// the live inputs; keeping it out of Render is what keeps Render pure and testable.
func Render(p sign.SignPlan, op sign.Op, env Env) (args []string, err error) {
	m, err := selectMechanism(p, env)
	if err != nil {
		return nil, err
	}
	return emit(p, op, m), nil
}

// mechanism is the resolved, single-principal way to sign — the solver's output.
type mechanism struct {
	class  sign.Class
	keyArg string // value for --key (key path / KMS URI / PKCS#11 URI); empty for oidc + FIDO2
	useSK  bool   // FIDO2 hardware token (cosign --sk)
}

func selectMechanism(p sign.SignPlan, env Env) (mechanism, error) {
	switch p.TrustClass {
	case sign.ClassKey:
		path := resolveKeyRef(p.KeyRef)
		if path == "" {
			return mechanism{}, fmt.Errorf("key class: key reference %q does not resolve to a usable key", p.KeyRef)
		}
		return mechanism{class: sign.ClassKey, keyArg: path}, nil

	case sign.ClassKMS:
		uri := resolveKMSURI(p.KMSRef)
		if uri == "" {
			return mechanism{}, fmt.Errorf("kms class: ref %q is not bound to a URI (set %s)", p.KMSRef, kmsEnvVar(p.KMSRef))
		}
		return mechanism{class: sign.ClassKMS, keyArg: uri}, nil

	case sign.ClassOIDC:
		// Keyless: the signer is the ambient OIDC identity (CI/workload token),
		// not an enumerated key — there is no --key to emit.
		return mechanism{class: sign.ClassOIDC}, nil

	case sign.ClassHardware:
		return selectHardware(p, env)

	default:
		return mechanism{}, fmt.Errorf("unknown trust class %q", p.TrustClass)
	}
}

// selectHardware is the |D| solver proper: hardware is the class where the Env
// genuinely enumerates multiple physical witnesses, so distinct-principal counting
// is load-bearing here.
func selectHardware(p sign.SignPlan, env Env) (mechanism, error) {
	// A reach is one transport to a principal; FIDO2 is preferred over PKCS#11.
	byPrincipal := map[Principal][]reach{}
	for _, d := range env.FIDO2 {
		if assuranceMet(d.PhysicalPresence, d.NonExportable, p) {
			byPrincipal[d.Principal] = append(byPrincipal[d.Principal], reach{useSK: true})
		}
	}
	for _, s := range env.PKCS11 {
		if assuranceMet(s.PhysicalPresence, s.NonExportable, p) {
			byPrincipal[s.Principal] = append(byPrincipal[s.Principal], reach{uri: s.URI})
		}
	}

	switch len(byPrincipal) {
	case 0:
		return mechanism{}, fmt.Errorf("hardware class: no declared device satisfies the required assurance (physical_presence=%v non_exportable=%v)",
			p.RequiresPhysicalPresence, p.RequiresNonExportableKey)
	case 1:
		var principal Principal
		for k := range byPrincipal {
			principal = k
		}
		return resolveTransport(byPrincipal[principal]), nil
	default:
		names := make([]string, 0, len(byPrincipal))
		for k := range byPrincipal {
			names = append(names, string(k))
		}
		sort.Strings(names)
		return mechanism{}, fmt.Errorf("hardware class: %d distinct keys satisfy the contract (%s) — a trust ambiguity; narrow the environment to exactly one",
			len(byPrincipal), strings.Join(names, ", "))
	}
}

// resolveTransport picks, for a single principal reachable several ways, a
// DETERMINISTIC transport: FIDO2 (--sk) first, else the lexicographically smallest
// PKCS#11 URI. Deterministic so the same Env always renders the same invocation.
func resolveTransport(reaches []reach) mechanism {
	uris := make([]string, 0, len(reaches))
	for _, r := range reaches {
		if r.useSK {
			return mechanism{class: sign.ClassHardware, useSK: true}
		}
		uris = append(uris, r.uri)
	}
	sort.Strings(uris)
	return mechanism{class: sign.ClassHardware, keyArg: uris[0]}
}

// reach must be visible to resolveTransport; declared at package scope so the
// helper and selectHardware share the type.
type reach struct {
	useSK bool
	uri   string
}

func assuranceMet(physicalPresence, nonExportable bool, p sign.SignPlan) bool {
	if p.RequiresPhysicalPresence && !physicalPresence {
		return false
	}
	if p.RequiresNonExportableKey && !nonExportable {
		return false
	}
	return true
}

// emit maps a resolved mechanism + plan + op to cosign argv (trust + policy flags
// only). The op target is appended by the executor.
func emit(p sign.SignPlan, op sign.Op, m mechanism) []string {
	args := []string{opVerb(op)}
	switch m.class {
	case sign.ClassKey, sign.ClassKMS:
		args = append(args, "--key", m.keyArg)
	case sign.ClassHardware:
		if m.useSK {
			args = append(args, "--sk")
		} else {
			args = append(args, "--key", m.keyArg)
		}
	case sign.ClassOIDC:
		// keyless — no --key flag
	}
	// Transparency-log control (cosign v3 semantics). cosign v3 defaults to a
	// TUF-provided signing-config that includes Rekor, and the legacy
	// --tlog-upload flag is deprecated + rejected alongside it. So:
	//   - transparency required → let the signing-config supply Rekor.
	//   - no transparency       → disable the signing-config so cosign never
	//                             reaches for Rekor, and skip the tlog explicitly
	//                             (the offline key/kms/hardware path).
	if p.TransparencyRequired {
		args = append(args, "--use-signing-config=true")
	} else {
		args = append(args, "--use-signing-config=false", "--tlog-upload=false")
	}
	// Image ops attach the signature/attestation to the registry; sign-blob is
	// detached and takes no --upload.
	if op == sign.OpSignImage || op == sign.OpAttest {
		args = append(args, "--yes")
	}
	return args
}

// opVerb is the cosign subcommand for a signing op.
func opVerb(op sign.Op) string {
	switch op {
	case sign.OpSignImage:
		return "sign"
	case sign.OpAttest:
		return "attest"
	case sign.OpSignBlob:
		return "sign-blob"
	default:
		return string(op)
	}
}

// resolveKeyRef mirrors sign.resolveKeyRef's logic at render time: "env:VAR" → the
// environment value; otherwise a filesystem path passed through (existence was
// already gated by sign.Enabled). Empty result means unresolved.
func resolveKeyRef(ref string) string {
	if ref == "" {
		return ""
	}
	if v, ok := strings.CutPrefix(ref, "env:"); ok {
		return os.Getenv(v)
	}
	return ref
}

// resolveKMSURI binds a logical KMS ref to a concrete URI by PURE env substitution:
// ref → $SF_SIGN_KMS_<REF> → URI. Core never parses the provider scheme (vault://,
// awskms://, gcpkms://) — it lives only in the env value, opaque end to end.
func resolveKMSURI(ref string) string {
	return os.Getenv(kmsEnvVar(ref))
}

func kmsEnvVar(ref string) string {
	return "SF_SIGN_KMS_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(ref))
}
