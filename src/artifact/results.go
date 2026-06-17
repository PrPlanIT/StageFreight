// Determinism rule (system-wide invariant for all Outcome identities):
//
//   All Outcome identities (docker, binary, archive) are derived from the
//   final materialized bytes, never from construction process state.
//
// Consequences:
//   - The recording layer (this package) is pure. It does not compute
//     identity; it records whatever identity the build/push/archive layer
//     produced. If those layers are nondeterministic across identical
//     inputs, that is a build-system defect, not something the schema can
//     paper over.
//   - v2 is an observation system, not a verifier of reproducibility.
//     Cross-runner SHA256 drift would be honestly recorded as two
//     different outcomes; the schema does not flag the discrepancy.
//     Verification is a separate layer's responsibility.

package artifact

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/atomicfile"
)

// ResultsManifest records what was actually externalized by publish. It is
// cryptographically tied to the OutputsManifest it fulfills via
// IntentChecksum. Written by publish, read by narrate and external auditors.
type ResultsManifest struct {
	SchemaVersion  string   `json:"schema_version"`
	CompletedAt    string   `json:"completed_at"`
	IntentChecksum string   `json:"intent_checksum"`
	Results        []Result `json:"results"`
	Checksum       string   `json:"checksum,omitempty"`
}

// Result groups all outcomes for a single intent artifact. ArtifactID is
// typed (the ArtifactID alias, not bare string) so the cross-domain join
// key cannot be silently confused with a free-form string elsewhere in the
// codebase.
type Result struct {
	ArtifactID   ArtifactID `json:"artifact_id"`
	ArtifactName string     `json:"artifact_name"`
	Kind         string     `json:"kind"`
	Outcomes     []Outcome  `json:"outcomes"`
}

// Outcome is one observed fact about one execution event. Outcomes are
// atomic and independent: push success and signing success are separate
// Outcomes with separate Type values. They are NOT combined fields of a
// single record. That separation is load-bearing — collapsing them would
// let a missing attestation be silently encoded as a successful push
// outcome.
//
// Discriminated union: Type selects which of the kind-specific sub-pointers
// is populated. Target is nil-able because not every outcome type has a
// target (binary build, archive build) — see Type-vs-Target rule in
// validateOutcome.
type Outcome struct {
	Type   OutcomeType    `json:"type"`
	Target *OutcomeTarget `json:"target,omitempty"`

	Push          *PushOutcome          `json:"push,omitempty"`
	Attestation   *AttestationOutcome   `json:"attestation,omitempty"`
	Binary        *BinaryOutcome        `json:"binary,omitempty"`
	Archive       *ArchiveOutcome       `json:"archive,omitempty"`
	BlobSignature *BlobSignatureOutcome `json:"blob_signature,omitempty"`
}

// OutcomeType discriminates the Outcome sub-kind. Closed enum.
type OutcomeType string

const (
	OutcomeTypePush          OutcomeType = "push"
	OutcomeTypeAttestation   OutcomeType = "attestation"
	OutcomeTypeBinaryBuild   OutcomeType = "binary_build"
	OutcomeTypeArchive       OutcomeType = "archive_build"
	OutcomeTypeBlobSignature OutcomeType = "blob_signature"
)

// Valid reports whether t is one of the defined OutcomeType values.
func (t OutcomeType) Valid() bool {
	switch t {
	case OutcomeTypePush, OutcomeTypeAttestation, OutcomeTypeBinaryBuild, OutcomeTypeArchive,
		OutcomeTypeBlobSignature:
		return true
	}
	return false
}

// outcomeTypeHasTarget reports whether Type requires a non-nil Target.
// Push and attestation are target-scoped (registry endpoint); binary,
// archive, and blob_signature are un-targeted by design (the build artifact
// — or its detached signature — is the truth; distribution targets are
// decided at release time in a separate subsystem).
func outcomeTypeHasTarget(t OutcomeType) bool {
	switch t {
	case OutcomeTypePush, OutcomeTypeAttestation:
		return true
	}
	return false
}

// PushOutcome is the result of pushing one artifact reference to one
// registry target. Independent of any later signing/attestation activity.
type PushOutcome struct {
	Status         OutcomeStatus `json:"status"`
	Digest         string        `json:"digest,omitempty"`
	ObservedDigest string        `json:"observed_digest,omitempty"`
	ObservedBy     string        `json:"observed_by,omitempty"`
	Error          string        `json:"error,omitempty"`
}

// TrustEvidence is the resolved assurance a signature actually carried, recorded
// at sign time so a consumer can answer "what did this signature attest?" from the
// manifest alone — never reducing a signing event to signed=true. These are facts
// resolved from the SignPlan, not a re-adjudication (that is the verification
// phase's job); recording them now is pure serialization-of-facts.
type TrustEvidence struct {
	TrustClass       string `json:"trust_class,omitempty"`       // key | oidc | kms | hardware
	Tier             string `json:"tier,omitempty"`              // assurance tier, e.g. "tier0-software" (auto-provisioned); empty = operator-supplied
	PhysicalPresence bool   `json:"physical_presence,omitempty"` // signer demonstrated physical presence
	NonExportable    bool   `json:"non_exportable,omitempty"`    // signing key was hardware-bound / non-exportable
	Transparency     bool   `json:"transparency,omitempty"`      // recorded in a transparency log
	SignerRef        string `json:"signer_ref,omitempty"`        // signer identity material (key/kms ref, oidc identity)
}

// AttestationOutcome is the result of attempting to sign or attest an
// already-published reference. Linked to the corresponding PushOutcome via
// shared ArtifactID + Target — never embedded inside it.
type AttestationOutcome struct {
	Status         OutcomeStatus `json:"status"`
	Kind           string        `json:"kind,omitempty"` // "cosign" | "in_toto" | "slsa"
	SignatureRef   string        `json:"signature_ref,omitempty"`
	AttestationRef string        `json:"attestation_ref,omitempty"`
	VerifiedDigest string        `json:"verified_digest,omitempty"`
	TrustEvidence
	Error string `json:"error,omitempty"`
}

// BlobSignatureOutcome is the result of signing a detached blob (e.g.
// SHA256SUMS) with cosign sign-blob. Un-targeted by design — a blob signature
// is the truth about the file's bytes, not about any registry endpoint, so it
// carries the blob + detached-signature paths and the trust class that signed
// it, never an OutcomeTarget. Class is the resolved trust class (key | oidc |
// kms | hardware), so a consumer can tell *how* the bytes were vouched for.
type BlobSignatureOutcome struct {
	Status        OutcomeStatus `json:"status"`
	Kind          string        `json:"kind,omitempty"` // signer mechanism, e.g. "cosign"
	BlobPath      string        `json:"blob_path,omitempty"`
	SignaturePath string        `json:"signature_path,omitempty"`
	TrustEvidence
	Error string `json:"error,omitempty"`
}

// BinaryOutcome is the result of building one binary artifact. The
// authoritative identity event for binaries — there is no separate "push
// to registry" step. SHA256 is the binary's content hash, computed at
// build time over the final on-disk artifact bytes.
//
// Per the unified determinism rule, SHA256 reflects whatever the build
// produced; the recording layer does not verify or normalize.
type BinaryOutcome struct {
	Status  OutcomeStatus `json:"status"`
	SHA256  string        `json:"sha256,omitempty"`
	Path    string        `json:"path,omitempty"`
	Size    int64         `json:"size,omitempty"`
	BuildID string        `json:"build_id,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// ArchiveOutcome is the result of building one archive artifact (a packaging
// operation over one or more binary artifacts). Sources references source
// binaries by ArtifactID — sibling relationship, not embedding. Sources is
// semantically unordered (treat as a set); on-disk serialization sorts it
// for determinism but consumer logic MUST NOT depend on the order.
//
// Size is parallel to BinaryOutcome.Size — a build-time observation of the
// final archive file's byte length. It is recorded here so consumers
// (release_create's BinaryRow construction) don't have to re-stat the file
// at consumer time, which would leak build-layer I/O into the release layer.
type ArchiveOutcome struct {
	Status  OutcomeStatus `json:"status"`
	SHA256  string        `json:"sha256,omitempty"`
	Path    string        `json:"path,omitempty"`
	Format  string        `json:"format,omitempty"` // "tar.gz" | "zip" | ...
	Size    int64         `json:"size,omitempty"`
	Sources []ArtifactID  `json:"sources,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// OutcomeTarget identifies the specific target an outcome refers to. Mirrors
// (a slice of) the intent Target — kind + the identifying coordinates.
type OutcomeTarget struct {
	Kind string `json:"kind"`
	Host string `json:"host,omitempty"`
	Path string `json:"path,omitempty"`
	Tag  string `json:"tag,omitempty"`
}

// OutcomeStatus is a closed enum applied per-sub-outcome. Push and
// attestation each carry their own Status — never shared.
type OutcomeStatus string

const (
	OutcomeSuccess OutcomeStatus = "success"
	OutcomeFailed  OutcomeStatus = "failed"
	OutcomeSkipped OutcomeStatus = "skipped"
)

// Valid reports whether s is one of the defined OutcomeStatus values. Empty
// is invalid: every recorded sub-outcome must declare a status.
func (s OutcomeStatus) Valid() bool {
	switch s {
	case OutcomeSuccess, OutcomeFailed, OutcomeSkipped:
		return true
	}
	return false
}

const (
	ResultsManifestPath  = ".stagefreight/published.json"
	ResultsSchemaVersion = "2"
)

var (
	ErrResultsManifestNotFound = errors.New("results manifest not found")
	ErrResultsManifestInvalid  = errors.New("results manifest invalid")
)

// normalizeResults applies deterministic ordering to fields that are
// semantically unordered. Specifically: ArchiveOutcome.Sources is a SET
// of source binary ArtifactIDs; consumer logic MUST NOT depend on its
// order, and on-disk serialization sorts it so the canonical checksum is
// stable across runs that recorded sources in different orders.
func normalizeResults(results []Result) {
	for i := range results {
		for j := range results[i].Outcomes {
			o := &results[i].Outcomes[j]
			if o.Archive != nil && len(o.Archive.Sources) > 1 {
				sort.Slice(o.Archive.Sources, func(a, b int) bool {
					return o.Archive.Sources[a] < o.Archive.Sources[b]
				})
			}
		}
	}
}

// Finalize populates derived fields, validates structure, and computes the
// embedded SHA-256 checksum. Mirrors OutputsManifest.Finalize.
func (m *ResultsManifest) Finalize() error {
	if m.SchemaVersion == "" {
		m.SchemaVersion = ResultsSchemaVersion
	}
	if m.SchemaVersion != ResultsSchemaVersion {
		return fmt.Errorf("%w: unsupported schema_version %q",
			ErrResultsManifestInvalid, m.SchemaVersion)
	}
	if m.CompletedAt == "" {
		m.CompletedAt = nowUTC()
	} else if err := validateRFC3339(m.CompletedAt, "completed_at", ErrResultsManifestInvalid); err != nil {
		return err
	}
	if strings.TrimSpace(m.IntentChecksum) == "" {
		return fmt.Errorf("%w: intent_checksum is required (ties results to approved intent)",
			ErrResultsManifestInvalid)
	}
	normalizeResults(m.Results)
	if err := validateResults(m.Results, ErrResultsManifestInvalid); err != nil {
		return err
	}
	m.Checksum = ""
	canonical, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling results manifest: %w", err)
	}
	m.Checksum = sha256Hex(canonical)
	return nil
}

// WriteResultsManifest finalizes (if needed) the manifest and writes it
// atomically to disk.
func WriteResultsManifest(dir string, manifest ResultsManifest) error {
	if err := manifest.Finalize(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling results manifest: %w", err)
	}
	data = append(data, '\n')

	path := filepath.Join(dir, ResultsManifestPath)
	if err := atomicfile.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing results manifest: %w", err)
	}
	return nil
}

// ReadResultsManifest reads and verifies the results manifest. Verification
// fails on schema mismatch, malformed JSON, RFC3339 violation, enum
// violation, or checksum mismatch.
func ReadResultsManifest(dir string) (*ResultsManifest, error) {
	path := filepath.Join(dir, ResultsManifestPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrResultsManifestNotFound
		}
		return nil, fmt.Errorf("%w: reading manifest: %v", ErrResultsManifestInvalid, err)
	}

	var manifest ResultsManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("%w: parsing manifest: %v", ErrResultsManifestInvalid, err)
	}

	if manifest.SchemaVersion != ResultsSchemaVersion {
		return nil, fmt.Errorf("%w: unsupported schema_version %q (expected %q)",
			ErrResultsManifestInvalid, manifest.SchemaVersion, ResultsSchemaVersion)
	}
	if err := validateRFC3339(manifest.CompletedAt, "completed_at", ErrResultsManifestInvalid); err != nil {
		return nil, err
	}
	if strings.TrimSpace(manifest.IntentChecksum) == "" {
		return nil, fmt.Errorf("%w: intent_checksum is required", ErrResultsManifestInvalid)
	}
	if manifest.Checksum == "" {
		return nil, fmt.Errorf("%w: missing embedded checksum", ErrResultsManifestInvalid)
	}
	if err := validateResults(manifest.Results, ErrResultsManifestInvalid); err != nil {
		return nil, err
	}

	expected := manifest.Checksum
	manifest.Checksum = ""
	canonical, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%w: re-marshaling for verification: %v", ErrResultsManifestInvalid, err)
	}
	if got := sha256Hex(canonical); got != expected {
		return nil, fmt.Errorf("%w: checksum mismatch (expected %s, got %s)",
			ErrResultsManifestInvalid, expected, got)
	}
	manifest.Checksum = expected
	return &manifest, nil
}

func validateResults(results []Result, errType error) error {
	for i, r := range results {
		if strings.TrimSpace(string(r.ArtifactID)) == "" {
			return fmt.Errorf("%w: result[%d]: artifact_id is required", errType, i)
		}
		if strings.TrimSpace(r.Kind) == "" {
			return fmt.Errorf("%w: result[%d]: kind is required", errType, i)
		}
		expectedID := NewArtifactID(r.Kind, r.ArtifactName)
		if r.ArtifactID != expectedID {
			return fmt.Errorf("%w: result[%d]: artifact_id %q does not match kind/name (expected %q)",
				errType, i, r.ArtifactID, expectedID)
		}
		if len(r.Outcomes) == 0 {
			return fmt.Errorf("%w: result %q: at least one outcome required",
				errType, r.ArtifactID)
		}
		for j, o := range r.Outcomes {
			if err := validateOutcome(&o, r.ArtifactID, j, errType); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateOutcome enforces three discriminated-union invariants:
//
//  1. Type is a valid enum value.
//  2. Type ↔ Target presence: push/attestation outcomes require a non-nil
//     Target; binary/archive outcomes require a nil Target. This is a hard
//     schema boundary that prevents docker semantics (registry targets)
//     from leaking into the binary domain — no fake "release_asset"
//     targets, no pseudo-local-file targets.
//  3. Exactly one kind-specific sub-pointer is non-nil and matches Type.
//     Sub-status is then validated against the closed status enum.
func validateOutcome(o *Outcome, artifactID ArtifactID, idx int, errType error) error {
	if !o.Type.Valid() {
		return fmt.Errorf("%w: result %q outcome[%d]: invalid type %q",
			errType, artifactID, idx, o.Type)
	}

	// Type ↔ Target presence rule. Locked invariant for Phase 4.
	if outcomeTypeHasTarget(o.Type) {
		if o.Target == nil {
			return fmt.Errorf("%w: result %q outcome[%d]: type %q requires a non-nil target",
				errType, artifactID, idx, o.Type)
		}
		if strings.TrimSpace(o.Target.Kind) == "" {
			return fmt.Errorf("%w: result %q outcome[%d]: target.kind is required",
				errType, artifactID, idx)
		}
	} else if o.Target != nil {
		return fmt.Errorf("%w: result %q outcome[%d]: type %q forbids a target (binary/archive are un-targeted by design)",
			errType, artifactID, idx, o.Type)
	}

	set := []string{}
	if o.Push != nil {
		set = append(set, string(OutcomeTypePush))
	}
	if o.Attestation != nil {
		set = append(set, string(OutcomeTypeAttestation))
	}
	if o.Binary != nil {
		set = append(set, string(OutcomeTypeBinaryBuild))
	}
	if o.Archive != nil {
		set = append(set, string(OutcomeTypeArchive))
	}
	if o.BlobSignature != nil {
		set = append(set, string(OutcomeTypeBlobSignature))
	}
	if len(set) == 0 {
		return fmt.Errorf("%w: result %q outcome[%d]: no sub-outcome set (expected %q)",
			errType, artifactID, idx, o.Type)
	}
	if len(set) > 1 {
		return fmt.Errorf("%w: result %q outcome[%d]: multiple sub-outcomes set %v (expected only %q)",
			errType, artifactID, idx, set, o.Type)
	}
	if set[0] != string(o.Type) {
		return fmt.Errorf("%w: result %q outcome[%d]: sub-outcome %q does not match type %q",
			errType, artifactID, idx, set[0], o.Type)
	}

	switch o.Type {
	case OutcomeTypePush:
		if !o.Push.Status.Valid() {
			return fmt.Errorf("%w: result %q outcome[%d]: push.status invalid %q",
				errType, artifactID, idx, o.Push.Status)
		}
	case OutcomeTypeAttestation:
		if !o.Attestation.Status.Valid() {
			return fmt.Errorf("%w: result %q outcome[%d]: attestation.status invalid %q",
				errType, artifactID, idx, o.Attestation.Status)
		}
	case OutcomeTypeBinaryBuild:
		if !o.Binary.Status.Valid() {
			return fmt.Errorf("%w: result %q outcome[%d]: binary.status invalid %q",
				errType, artifactID, idx, o.Binary.Status)
		}
	case OutcomeTypeArchive:
		if !o.Archive.Status.Valid() {
			return fmt.Errorf("%w: result %q outcome[%d]: archive.status invalid %q",
				errType, artifactID, idx, o.Archive.Status)
		}
	case OutcomeTypeBlobSignature:
		if !o.BlobSignature.Status.Valid() {
			return fmt.Errorf("%w: result %q outcome[%d]: blob_signature.status invalid %q",
				errType, artifactID, idx, o.BlobSignature.Status)
		}
	}
	return nil
}
