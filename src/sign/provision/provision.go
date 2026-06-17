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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

	fp, err := fingerprint(filepath.Join(stateDir, id.PubFile))
	if err != nil {
		return nil, err
	}
	if fp != id.Fingerprint {
		return nil, fmt.Errorf("signing identity drift: public-key fingerprint %s does not match recorded %s — refusing to continue (trust continuity over convenience)", fp, id.Fingerprint)
	}
	return &id, nil
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

func fingerprint(pubPath string) (string, error) {
	b, err := os.ReadFile(pubPath)
	if err != nil {
		return "", fmt.Errorf("reading public key: %w", err)
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
