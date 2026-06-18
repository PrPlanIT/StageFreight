// Package trustdisclosure interprets a publish ResultsManifest into a STRUCTURED
// trust disclosure — typed facts, never pre-baked prose. It is the first narrow slice
// of trust-CONSUMPTION logic lifted out of the release command, so the publisher stays
// a transcriber: the release renderer (and later an API, a policy UI, or the
// verification engine) formats these facts however it needs, rather than each
// re-deriving "the primary signature" or "which layers" from raw outcomes.
//
// Interpretation here is PURE — no filesystem, no config, no rendering. The continuity
// anchor is loaded at the edge (impure: state dir + identity) and passed IN, so this
// package is a total function of (results, anchor). That keeps the boundary the rest
// of the subsystem already holds: interpretation pure, acquisition impure. As the
// verification engine matures it will own this summarization; the release renderer is
// then a formatter around its output.
package trustdisclosure

import (
	"path/filepath"
	"sort"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/sign/provision"
)

// Disclosure is the structured trust disclosure for one release.
type Disclosure struct {
	Primary      *SignatureFact    // headline signature (Tier-0 first, else first); nil if no signatures
	Layers       []SignatureFact   // other signatures, deduped, primary excluded
	Attestations []AttestationFact // provenance attestations, deduped
	Anchor       *Anchor           // pinnable continuity anchor, when this release carries a Tier-0 sig + a loaded identity
}

// SignatureFact is one signature's recorded trust dimensions — the SAME type for the
// primary and for additional layers (a layer is just a non-primary signature).
type SignatureFact struct {
	Class            string // key | oidc | kms | hardware
	Tier             string // raw tier ("tier0-software", …); the renderer maps to a human label
	TrustDomain      string // oidc/keyless ecosystem ("public-sigstore", a label, a Fulcio host)
	SignerRef        string
	Transparency     bool
	NonExportable    bool
	PhysicalPresence bool
	Asset            string // detached-signature filename (blob) or image digest ref
	IsBlob           bool
}

// AttestationFact is one provenance attestation's recorded dimensions.
type AttestationFact struct {
	PredicateType    string
	Class            string
	Tier             string
	TrustDomain      string
	NonExportable    bool
	PhysicalPresence bool
	VerifiedDigest   string
}

// Anchor is the pinnable continuity identity — loaded at the edge and passed to Build.
type Anchor struct {
	Fingerprint string
	Asset       string // release-asset filename for the public key (e.g. "cosign.pub")
}

// Build interprets the results manifest into a structured disclosure. Pure: the caller
// loads the identity (if any) and passes it as anchor; Build attaches it only when this
// release actually carries a Tier-0 signature (never a stale anchor for a release it did
// not sign). Returns nil when there is nothing to disclose.
func Build(results *artifact.ResultsManifest, anchor *Anchor) *Disclosure {
	if results == nil {
		return nil
	}
	sigs := collectSignatures(results)
	atts := collectAttestations(results)
	if len(sigs) == 0 && len(atts) == 0 {
		return nil
	}
	d := &Disclosure{Attestations: atts}
	if len(sigs) > 0 {
		primary := sigs[0]
		d.Primary = &primary
		d.Layers = layersBeyondPrimary(sigs)
	}
	if anchor != nil && hasTier0(sigs) {
		d.Anchor = anchor
	}
	return d
}

// ChecksumSig returns the detached-checksum signature asset for a verify recipe — the
// first blob signature (Tier-0 preferred, since signatures are sorted Tier-0-first).
func (d *Disclosure) ChecksumSig() string {
	if d.Primary != nil && d.Primary.IsBlob {
		return d.Primary.Asset
	}
	for _, l := range d.Layers {
		if l.IsBlob {
			return l.Asset
		}
	}
	return ""
}

func collectSignatures(results *artifact.ResultsManifest) []SignatureFact {
	var sigs []SignatureFact
	for _, r := range results.Results {
		for _, o := range r.Outcomes {
			switch {
			case o.BlobSignature != nil && o.BlobSignature.Status == artifact.OutcomeSuccess:
				sigs = append(sigs, signatureFact(o.BlobSignature.TrustEvidence, filepath.Base(o.BlobSignature.SignaturePath), true))
			case o.Attestation != nil && o.Attestation.Status == artifact.OutcomeSuccess:
				sigs = append(sigs, signatureFact(o.Attestation.TrustEvidence, o.Attestation.SignatureRef, false))
			}
		}
	}
	// Tier-0 (the continuity anchor) sorts first → the disclosure primary when present.
	sort.SliceStable(sigs, func(i, j int) bool {
		return sigs[i].Tier == provision.TierSoftware && sigs[j].Tier != provision.TierSoftware
	})
	return sigs
}

func signatureFact(ev artifact.TrustEvidence, asset string, isBlob bool) SignatureFact {
	return SignatureFact{
		Class: ev.TrustClass, Tier: ev.Tier, TrustDomain: ev.TrustDomain, SignerRef: ev.SignerRef,
		Transparency: ev.Transparency, NonExportable: ev.NonExportable, PhysicalPresence: ev.PhysicalPresence,
		Asset: asset, IsBlob: isBlob,
	}
}

func collectAttestations(results *artifact.ResultsManifest) []AttestationFact {
	seen := map[AttestationFact]bool{}
	var out []AttestationFact
	for _, r := range results.Results {
		for _, o := range r.Outcomes {
			pa := o.ProvenanceAttestation
			if pa == nil || pa.Status != artifact.OutcomeSuccess {
				continue
			}
			f := AttestationFact{
				PredicateType: pa.PredicateType, Class: pa.TrustClass, Tier: pa.Tier,
				TrustDomain: pa.TrustDomain, NonExportable: pa.NonExportable,
				PhysicalPresence: pa.PhysicalPresence, VerifiedDigest: pa.VerifiedDigest,
			}
			if !seen[f] { // SignatureFact/AttestationFact are comparable structs — value-dedup
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// layersBeyondPrimary dedupes signatures (by value) and drops the primary (index 0).
func layersBeyondPrimary(sigs []SignatureFact) []SignatureFact {
	seen := map[SignatureFact]bool{}
	var uniq []SignatureFact
	for _, s := range sigs {
		if !seen[s] {
			seen[s] = true
			uniq = append(uniq, s)
		}
	}
	if len(uniq) <= 1 {
		return nil
	}
	return uniq[1:]
}

func hasTier0(sigs []SignatureFact) bool {
	for _, s := range sigs {
		if s.Tier == provision.TierSoftware {
			return true
		}
	}
	return false
}
