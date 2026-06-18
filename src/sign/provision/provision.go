// Package provision manages StageFreight's auto-provisioned Tier-0 signing
// identity — a persistent software cosign keypair living in the operator's signing
// state dir. Its defining property is TRUST CONTINUITY: the identity is generated
// exactly once, then reused forever; it is NEVER silently regenerated.
//
// Once signing is automatic and "always on," continuity of identity matters more
// than convenience — a silently-rotated key would quietly invalidate every
// previously published trust anchor. So every ambiguous state is FATAL, never
// papered over by regeneration:
//
//   - identity record present but private key missing  → fatal
//   - identity record present but public key missing   → fatal (partial state)
//   - public-key fingerprint drifted from the record   → fatal
//   - identity record corrupt/unparseable              → fatal
//   - key material present with no identity record      → fatal (orphan; refuse to provision over it)
//
// Recovery from any of these is a deliberate operator act (restore the material, or
// reset the state dir on purpose), never an automatic side effect. Tier-0 optimizes
// persistence + frictionless adoption, not maximum assurance — harden by climbing
// the custody ladder. See docs/architecture/signing-trust-model.md (O.3).
package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// TierSoftware is the assurance tier of an auto-provisioned software key —
	// recorded so a consumer can always discover the tier they actually operate
	// under (never implying stronger trust than exists).
	TierSoftware = "tier0-software"

	identityFile = "identity.json"
	keyFile      = "cosign.key"
	pubFile      = "cosign.pub"
)

// Identity is the persisted record of the signing identity — the source of truth
// for trust continuity, written as identity.json in the state dir.
type Identity struct {
	Tier            string `json:"tier"`
	AutoProvisioned bool   `json:"auto_provisioned"`
	CreatedAt       string `json:"created_at"`
	KeyFile         string `json:"key_file"`
	PubFile         string `json:"pub_file"`
	Fingerprint     string `json:"fingerprint"` // sha256:<hex> of the public-key bytes
}

// KeyGenerator produces a cosign keypair (keyFile/pubFile) inside dir. Injected so
// the continuity state machine is testable without a real cosign.
type KeyGenerator interface {
	GenerateKeyPair(ctx context.Context, dir, keyFile, pubFile string) error
}

// EnsureIdentity returns the Tier-0 signing identity in stateDir, provisioning it
// on first use and validating continuity on every use thereafter. `now` is recorded
// as the provision timestamp (injected for deterministic tests). Any continuity
// violation is returned as an error — the caller MUST treat it as fatal.
func EnsureIdentity(ctx context.Context, stateDir string, gen KeyGenerator, now string) (*Identity, error) {
	if stateDir == "" {
		return nil, errors.New("signing state dir is not configured")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("signing state dir %q: %w", stateDir, err)
	}

	id, err := loadAndValidate(stateDir)
	if err != nil {
		return nil, err // fatal continuity error — never regenerate over it
	}
	if id != nil {
		return id, nil // existing, validated — reuse
	}

	// First provision.
	if err := gen.GenerateKeyPair(ctx, stateDir, keyFile, pubFile); err != nil {
		return nil, fmt.Errorf("provisioning Tier-0 signing key: %w", err)
	}
	if err := secureKey(filepath.Join(stateDir, keyFile)); err != nil {
		return nil, err
	}
	fp, err := fingerprint(filepath.Join(stateDir, pubFile))
	if err != nil {
		return nil, err
	}
	id = &Identity{
		Tier:            TierSoftware,
		AutoProvisioned: true,
		CreatedAt:       now,
		KeyFile:         keyFile,
		PubFile:         pubFile,
		Fingerprint:     fp,
	}
	if err := writeIdentity(stateDir, id); err != nil {
		return nil, err
	}
	return id, nil
}

// loadAndValidate loads identity.json and enforces the continuity invariants.
// Returns (nil, nil) ONLY when the state dir is genuinely empty — the one case in
// which provisioning is allowed to run.
func loadAndValidate(stateDir string) (*Identity, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, identityFile))
	if errors.Is(err, fs.ErrNotExist) {
		// No record. Orphan key material (key/pub with no record) is ambiguous —
		// refuse to provision over it rather than risk masking a restore-in-progress.
		if exists(filepath.Join(stateDir, keyFile)) || exists(filepath.Join(stateDir, pubFile)) {
			return nil, fmt.Errorf("signing state %q holds key material but no identity record — refusing to provision over it; reset the state dir deliberately", stateDir)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading signing identity: %w", err)
	}

	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return nil, fmt.Errorf("signing identity record is corrupt (%w) — refusing to regenerate; fix or reset the state dir deliberately", err)
	}

	keyOK := exists(filepath.Join(stateDir, id.KeyFile))
	pubOK := exists(filepath.Join(stateDir, id.PubFile))
	if !keyOK || !pubOK {
		return nil, fmt.Errorf("signing identity %s is partial (private key present=%v, public key present=%v) — refusing to regenerate; restore the missing material or reset deliberately", id.Fingerprint, keyOK, pubOK)
	}
	// Re-tighten the private key on every reuse — defends against a perms drift
	// (a backup/restore or volume remount that loosened it).
	if err := secureKey(filepath.Join(stateDir, id.KeyFile)); err != nil {
		return nil, err
	}

	fp, err := fingerprint(filepath.Join(stateDir, id.PubFile))
	if err != nil {
		return nil, err
	}
	if fp != id.Fingerprint {
		return nil, fmt.Errorf("signing identity drift: public-key fingerprint %s does not match recorded %s — refusing to continue (trust continuity over convenience)", fp, id.Fingerprint)
	}
	return &id, nil
}

// LoadIdentity reads the persisted identity record (read-only; no provisioning) so
// the publish phase can obtain the public anchor + fingerprint to attach and
// disclose. Returns (nil, nil) when the state dir has no identity, and validates
// the same continuity invariants as the provisioning path (a partial/drifted
// identity must not be published as if intact).
func LoadIdentity(stateDir string) (*Identity, error) {
	if stateDir == "" {
		return nil, nil
	}
	return loadAndValidate(stateDir)
}

// KeyPath returns the absolute path to the persisted private key for this identity.
func (id *Identity) KeyPath(stateDir string) string {
	return filepath.Join(stateDir, id.KeyFile)
}

// PubPath returns the absolute path to the public trust anchor for this identity.
func (id *Identity) PubPath(stateDir string) string {
	return filepath.Join(stateDir, id.PubFile)
}

func writeIdentity(stateDir string, id *Identity) error {
	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir, identityFile), data, 0o600)
}

// fingerprint hashes the SEMANTIC public-key material, not the raw file bytes —
// it decodes the PEM and hashes the DER (SPKI) block, so a CRLF/LF conversion,
// trailing-newline change, or re-wrapping of the PEM does NOT spuriously trip the
// continuity drift check and brick signing. A non-PEM file falls back to hashing
// raw bytes (still deterministic).
func fingerprint(pubPath string) (string, error) {
	b, err := os.ReadFile(pubPath)
	if err != nil {
		return "", fmt.Errorf("reading public key: %w", err)
	}
	material := b
	if block, _ := pem.Decode(b); block != nil {
		material = block.Bytes
	}
	sum := sha256.Sum256(material)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// secureKey enforces owner-only (0600) permissions on a persisted private key —
// the file is the protection boundary for a Tier-0 (empty-password) key, so it must
// never be group/world readable.
func secureKey(keyPath string) error {
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return fmt.Errorf("securing private key permissions (%s): %w", keyPath, err)
	}
	return nil
}

// GuardStateDir refuses a signing state dir that lives inside the repository / build
// context (repoRoot). Persisting a private signing key in the source tree risks it
// being committed, baked into a docker image layer, or swept into a published
// artifact — the key MUST live on a durable volume outside the repo. Callers invoke
// this before provisioning.
func GuardStateDir(stateDir, repoRoot string) error {
	sd, err := filepath.Abs(stateDir)
	if err != nil {
		return fmt.Errorf("resolving signing state dir: %w", err)
	}
	rr, err := filepath.Abs(repoRoot)
	if err != nil {
		return fmt.Errorf("resolving repo root: %w", err)
	}
	rel, err := filepath.Rel(rr, sd)
	if err != nil {
		return nil // different volumes / unrelatable → genuinely outside, fine
	}
	if rel == "." || !strings.HasPrefix(rel, "..") {
		return fmt.Errorf("signing state dir %q is inside the repository (%q) — refusing to persist a private signing key in the source tree; set signing.state_dir to a durable path outside the repo", sd, rr)
	}
	return nil
}

// IsPrivateKeyPath reports whether path looks like signing key material — a
// defensive tripwire so a private key (or the identity record) is never accidentally
// uploaded/published as an artifact. Belt-and-suspenders: structurally, only public
// signatures/anchors are ever added to asset lists, but this guards future mistakes.
func IsPrivateKeyPath(path string) bool {
	base := filepath.Base(path)
	return base == keyFile || base == identityFile || strings.HasSuffix(base, ".key")
}
