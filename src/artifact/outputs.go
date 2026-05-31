package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/atomicfile"
)

// OutputsManifest is the immutable description of the outputs produced by
// perform and the publication requirements approved by review. It is written
// by the perform phase, read by review and publish, and never modified after
// perform completes. Publish records observed outcomes in a separate
// ResultsManifest tied to this file via intent_checksum.
//
// Determinism is a property of the type system here, not the encoder:
// all fields are strictly typed, struct field order is fixed, and no maps
// appear in the schema. encoding/json's standard MarshalIndent is sufficient
// to produce byte-deterministic output.
type OutputsManifest struct {
	SchemaVersion string     `json:"schema_version"`
	GeneratedAt   string     `json:"generated_at"`
	Commit        string     `json:"commit,omitempty"`
	Pipeline      *Pipeline  `json:"pipeline,omitempty"`
	Artifacts     []Artifact `json:"artifacts"`
	Checksum      string     `json:"checksum,omitempty"`
}

// Pipeline captures the CI context that produced the manifest.
type Pipeline struct {
	ID       string `json:"id,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// Artifact is a single output produced by perform. ID is the stable identity
// used to correlate intent ↔ result; Name is human-friendly. Exactly one of
// the kind-specific descriptor pointers is set, matching Kind.
//
// ID is typed (ArtifactID, not bare string) so identity values cannot be
// assembled inline from fields elsewhere in the codebase. The only approved
// constructor is NewArtifactID.
type Artifact struct {
	ID      ArtifactID `json:"id"`
	Kind    string     `json:"kind"`
	Name    string     `json:"name"`
	Version string     `json:"version,omitempty"`

	Docker  *DockerDescriptor  `json:"docker,omitempty"`
	Binary  *BinaryDescriptor  `json:"binary,omitempty"`
	Archive *ArchiveDescriptor `json:"archive,omitempty"`

	Targets []Target `json:"targets"`
}

// DockerDescriptor describes a docker image to be built.
type DockerDescriptor struct {
	Dockerfile      string   `json:"dockerfile"`
	Context         string   `json:"context"`
	Platforms       []string `json:"platforms"`
	BuildArgsDigest string   `json:"build_args_digest,omitempty"`
}

// BinaryDescriptor describes a compiled binary artifact. Plan-time intent
// only — SHA256 and final digest are observed at build time and recorded in
// the corresponding Outcome, not here.
type BinaryDescriptor struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Path      string `json:"path"`
	Toolchain string `json:"toolchain,omitempty"`
}

// ArchiveDescriptor describes a packaged archive artifact. Plan-time intent
// only — content SHA256 is observed at archive time and recorded in the
// corresponding Outcome.
type ArchiveDescriptor struct {
	Format string `json:"format"`
	Path   string `json:"path"`
}

// Target is one destination an artifact must be distributed to. Exactly one
// of the kind-specific target pointers is set, matching Kind. Credentials
// are not part of this contract — they are deployment configuration.
type Target struct {
	Kind string `json:"kind"`

	Registry          *RegistryTarget          `json:"registry,omitempty"`
	ForgeReleaseAsset *ForgeReleaseAssetTarget `json:"forge_release_asset,omitempty"`

	Requirements Requirements `json:"requirements,omitempty"`
}

// RegistryTarget describes a container registry destination.
type RegistryTarget struct {
	Host string   `json:"host"`
	Path string   `json:"path"`
	Tags []string `json:"tags"`
}

// ForgeReleaseAssetTarget describes a forge release asset destination
// (GitLab/GitHub/Gitea release with attached file).
type ForgeReleaseAssetTarget struct {
	AssetName string `json:"asset_name"`
}

// Requirements expresses publication requirements approved by review. Adding
// a new requirement is a typed field addition (backward compatible: zero
// value = not required).
type Requirements struct {
	Sign   bool `json:"sign,omitempty"`
	Attest bool `json:"attest,omitempty"`
	SBOM   bool `json:"sbom,omitempty"`
}

const (
	OutputsManifestPath  = ".stagefreight/outputs.json"
	OutputsSchemaVersion = "2"
)

var (
	ErrOutputsManifestNotFound = errors.New("outputs manifest not found")
	ErrOutputsManifestInvalid  = errors.New("outputs manifest invalid")
)

// ArtifactID is the system-wide identity primitive. The typed string alias
// turns identity construction into a compile-visible gesture rather than a
// runtime string assembly — any code that needs an ArtifactID must obtain
// it from a manifest or a view, never assemble one from fields inline.
// This is the structural lock that prevents the friendly-name shortcut
// pattern (e.g. `binaryName + "-" + os + "-" + arch`) from reintroducing
// alternate identity systems.
//
// Format: "<kind>:<name>". The constructor NewArtifactID is the only
// approved way to mint a new ID inside the package; external callers
// receive IDs from view/manifest read operations.
type ArtifactID string

// NewArtifactID mints the stable identity of an artifact across reruns.
// Derived purely from (kind, name). Mutable inputs (commit SHA, pipeline
// ID, time) MUST NOT be embedded — that would defeat intent↔result
// correlation when publish runs in a separate job from perform.
// Uniqueness across the artifacts slice within a manifest is enforced at
// Write time.
func NewArtifactID(kind, name string) ArtifactID {
	return ArtifactID(kind + ":" + name)
}

// Finalize populates derived fields (schema_version, generated_at, artifact
// ids), validates and normalizes structure, and computes the embedded
// SHA-256 checksum. After a successful Finalize, the manifest is
// byte-deterministic and ready to be either written or used as the intent
// snapshot for a ResultsBuilder.
//
// Idempotent on already-finalized manifests when content has not changed.
// Mutates the receiver.
func (m *OutputsManifest) Finalize() error {
	if m.SchemaVersion == "" {
		m.SchemaVersion = OutputsSchemaVersion
	}
	if m.SchemaVersion != OutputsSchemaVersion {
		return fmt.Errorf("%w: unsupported schema_version %q", ErrOutputsManifestInvalid, m.SchemaVersion)
	}
	if m.GeneratedAt == "" {
		m.GeneratedAt = nowUTC()
	} else if err := validateRFC3339(m.GeneratedAt, "generated_at", ErrOutputsManifestInvalid); err != nil {
		return err
	}
	if err := normalizeArtifacts(m.Artifacts); err != nil {
		return err
	}

	m.Checksum = ""
	canonical, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling outputs manifest: %w", err)
	}
	m.Checksum = sha256Hex(canonical)
	return nil
}

// WriteOutputsManifest finalizes (if needed) the manifest and writes it
// atomically to disk.
func WriteOutputsManifest(dir string, manifest OutputsManifest) error {
	if err := manifest.Finalize(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling outputs manifest: %w", err)
	}
	data = append(data, '\n')

	path := filepath.Join(dir, OutputsManifestPath)
	if err := atomicfile.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing outputs manifest: %w", err)
	}
	return nil
}

// ReadOutputsManifest reads and verifies the outputs manifest. Verification
// fails on schema mismatch, malformed JSON, RFC3339 violation, enum
// violation, or checksum mismatch.
func ReadOutputsManifest(dir string) (*OutputsManifest, error) {
	path := filepath.Join(dir, OutputsManifestPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrOutputsManifestNotFound
		}
		return nil, fmt.Errorf("%w: reading manifest: %v", ErrOutputsManifestInvalid, err)
	}

	var manifest OutputsManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("%w: parsing manifest: %v", ErrOutputsManifestInvalid, err)
	}

	if manifest.SchemaVersion != OutputsSchemaVersion {
		return nil, fmt.Errorf("%w: unsupported schema_version %q (expected %q)",
			ErrOutputsManifestInvalid, manifest.SchemaVersion, OutputsSchemaVersion)
	}
	if err := validateRFC3339(manifest.GeneratedAt, "generated_at", ErrOutputsManifestInvalid); err != nil {
		return nil, err
	}
	if manifest.Checksum == "" {
		return nil, fmt.Errorf("%w: missing embedded checksum", ErrOutputsManifestInvalid)
	}
	if err := validateArtifacts(manifest.Artifacts, ErrOutputsManifestInvalid); err != nil {
		return nil, err
	}

	expected := manifest.Checksum
	manifest.Checksum = ""
	canonical, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%w: re-marshaling for verification: %v", ErrOutputsManifestInvalid, err)
	}
	if got := sha256Hex(canonical); got != expected {
		return nil, fmt.Errorf("%w: checksum mismatch (expected %s, got %s)",
			ErrOutputsManifestInvalid, expected, got)
	}
	manifest.Checksum = expected
	return &manifest, nil
}

// normalizeArtifacts validates required fields, populates derived fields,
// and enforces uniqueness of ArtifactID within the manifest. Write path.
func normalizeArtifacts(artifacts []Artifact) error {
	seenIDs := make(map[ArtifactID]struct{}, len(artifacts))
	for i := range artifacts {
		a := &artifacts[i]
		if strings.TrimSpace(a.Kind) == "" {
			return fmt.Errorf("%w: artifact[%d]: kind is required", ErrOutputsManifestInvalid, i)
		}
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("%w: artifact[%d]: name is required", ErrOutputsManifestInvalid, i)
		}
		expectedID := NewArtifactID(a.Kind, a.Name)
		if a.ID == "" {
			a.ID = expectedID
		} else if a.ID != expectedID {
			return fmt.Errorf("%w: artifact[%d]: id %q does not match kind/name (expected %q)",
				ErrOutputsManifestInvalid, i, a.ID, expectedID)
		}
		if _, dup := seenIDs[a.ID]; dup {
			return fmt.Errorf("%w: duplicate artifact id %q", ErrOutputsManifestInvalid, a.ID)
		}
		seenIDs[a.ID] = struct{}{}

		if err := validateDescriptorMatchesKind(a, ErrOutputsManifestInvalid); err != nil {
			return err
		}
		if err := validateKindTargetPresence(*a, ErrOutputsManifestInvalid); err != nil {
			return err
		}
		for j := range a.Targets {
			t := &a.Targets[j]
			if err := validateTarget(t, a.ID, j, ErrOutputsManifestInvalid); err != nil {
				return err
			}
			if t.Registry != nil {
				t.Registry.Host = normalizeHost(t.Registry.Host)
			}
		}
	}
	return nil
}

// kindRequiresTargets reports whether artifact kind k requires at least one
// target. Docker artifacts publish to registries, so a target IS the
// externalization destination. Binary and archive artifacts are un-targeted
// by design (Q2, Phase 4 design): the build artifact IS the truth, and any
// distribution destination is decided at a later layer (release_create),
// not at build time. Mirrors the outcome-side outcomeTypeHasTarget rule.
func kindRequiresTargets(k string) bool {
	return k == "docker"
}

// validateKindTargetPresence enforces the symmetric intent-side rule of
// outcomeTypeHasTarget: docker artifacts MUST have ≥1 target; binary and
// archive artifacts MUST have zero targets. This hard boundary prevents
// docker semantics (registry targets) from leaking into binary intent —
// no pseudo "release_asset" targets, no fake "local_file" targets.
func validateKindTargetPresence(a Artifact, errType error) error {
	if kindRequiresTargets(a.Kind) {
		if len(a.Targets) == 0 {
			return fmt.Errorf("%w: artifact %q: kind %q requires at least one target",
				errType, a.ID, a.Kind)
		}
		return nil
	}
	if len(a.Targets) > 0 {
		return fmt.Errorf("%w: artifact %q: kind %q forbids targets (un-targeted by design)",
			errType, a.ID, a.Kind)
	}
	return nil
}

// validateArtifacts is the read-path equivalent: enforces invariants without
// mutating. Re-runs descriptor/target shape checks since file contents could
// have been edited by hand or by a buggy writer.
func validateArtifacts(artifacts []Artifact, errType error) error {
	seenIDs := make(map[ArtifactID]struct{}, len(artifacts))
	for i, a := range artifacts {
		if strings.TrimSpace(a.Kind) == "" {
			return fmt.Errorf("%w: artifact[%d]: kind is required", errType, i)
		}
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("%w: artifact[%d]: name is required", errType, i)
		}
		if a.ID != NewArtifactID(a.Kind, a.Name) {
			return fmt.Errorf("%w: artifact[%d]: id %q does not match kind/name",
				errType, i, a.ID)
		}
		if _, dup := seenIDs[a.ID]; dup {
			return fmt.Errorf("%w: duplicate artifact id %q", errType, a.ID)
		}
		seenIDs[a.ID] = struct{}{}
		if err := validateDescriptorMatchesKind(&a, errType); err != nil {
			return err
		}
		if err := validateKindTargetPresence(a, errType); err != nil {
			return err
		}
		for j := range a.Targets {
			t := a.Targets[j]
			if err := validateTarget(&t, a.ID, j, errType); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateDescriptorMatchesKind enforces the discriminated-union invariant:
// exactly one of the kind-specific descriptor pointers is set, and it
// matches the Kind string.
func validateDescriptorMatchesKind(a *Artifact, errType error) error {
	set := []string{}
	if a.Docker != nil {
		set = append(set, "docker")
	}
	if a.Binary != nil {
		set = append(set, "binary")
	}
	if a.Archive != nil {
		set = append(set, "archive")
	}
	if len(set) == 0 {
		return fmt.Errorf("%w: artifact %q: no descriptor set (expected %q)",
			errType, a.ID, a.Kind)
	}
	if len(set) > 1 {
		return fmt.Errorf("%w: artifact %q: multiple descriptors set %v (expected only %q)",
			errType, a.ID, set, a.Kind)
	}
	if set[0] != a.Kind {
		return fmt.Errorf("%w: artifact %q: descriptor %q does not match kind %q",
			errType, a.ID, set[0], a.Kind)
	}
	return nil
}

// validateTarget enforces the discriminated-union invariant on targets and
// the kind-specific shape requirements.
func validateTarget(t *Target, artifactID ArtifactID, idx int, errType error) error {
	if strings.TrimSpace(t.Kind) == "" {
		return fmt.Errorf("%w: artifact %q target[%d]: kind is required", errType, artifactID, idx)
	}
	set := []string{}
	if t.Registry != nil {
		set = append(set, "registry")
	}
	if t.ForgeReleaseAsset != nil {
		set = append(set, "forge_release_asset")
	}
	if len(set) == 0 {
		return fmt.Errorf("%w: artifact %q target[%d]: no target body set (expected %q)",
			errType, artifactID, idx, t.Kind)
	}
	if len(set) > 1 {
		return fmt.Errorf("%w: artifact %q target[%d]: multiple target bodies set %v (expected only %q)",
			errType, artifactID, idx, set, t.Kind)
	}
	if set[0] != t.Kind {
		return fmt.Errorf("%w: artifact %q target[%d]: body %q does not match kind %q",
			errType, artifactID, idx, set[0], t.Kind)
	}
	if t.Registry != nil {
		if strings.TrimSpace(t.Registry.Host) == "" {
			return fmt.Errorf("%w: artifact %q target[%d]: registry.host is required",
				errType, artifactID, idx)
		}
		if strings.TrimSpace(t.Registry.Path) == "" {
			return fmt.Errorf("%w: artifact %q target[%d]: registry.path is required",
				errType, artifactID, idx)
		}
		if len(t.Registry.Tags) == 0 {
			return fmt.Errorf("%w: artifact %q target[%d]: registry.tags must be non-empty",
				errType, artifactID, idx)
		}
	}
	if t.ForgeReleaseAsset != nil {
		if strings.TrimSpace(t.ForgeReleaseAsset.AssetName) == "" {
			return fmt.Errorf("%w: artifact %q target[%d]: forge_release_asset.asset_name is required",
				errType, artifactID, idx)
		}
	}
	return nil
}

// nowUTC returns the current time in RFC3339 UTC. All v2 manifest timestamps
// pass through this helper to guarantee one format across the codebase.
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// validateRFC3339 returns a wrapped error if s is non-empty and not RFC3339.
func validateRFC3339(s, field string, errType error) error {
	if s == "" {
		return fmt.Errorf("%w: %s is required", errType, field)
	}
	if _, err := time.Parse(time.RFC3339, s); err != nil {
		return fmt.Errorf("%w: %s must be RFC3339, got %q", errType, field, s)
	}
	return nil
}

// sha256Hex returns the hex SHA-256 of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// normalizeHost strips scheme/trailing slash and lowercases a registry host
// so identical hosts compare equal regardless of input shape.
func normalizeHost(h string) string {
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	h = strings.TrimSuffix(h, "/")
	return strings.ToLower(h)
}
