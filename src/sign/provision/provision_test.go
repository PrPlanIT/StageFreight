package provision

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGen writes deterministic key material so the continuity state machine can be
// exercised without a real cosign.
type fakeGen struct {
	calls int
	pub   string // pub bytes to write (varies per call when set)
}

func (f *fakeGen) GenerateKeyPair(_ context.Context, dir, keyFile, pubFile string) error {
	f.calls++
	pub := "PUBKEY-A"
	if f.pub != "" {
		pub = f.pub
	}
	if err := os.WriteFile(filepath.Join(dir, keyFile), []byte("PRIVKEY"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, pubFile), []byte(pub), 0o644)
}

func TestEnsureIdentity_ProvisionsOnceThenReuses(t *testing.T) {
	dir := t.TempDir()
	gen := &fakeGen{}

	id1, err := EnsureIdentity(context.Background(), dir, gen, "2026-06-17T00:00:00Z")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if id1.Tier != TierSoftware || !id1.AutoProvisioned || id1.Fingerprint == "" {
		t.Fatalf("unexpected identity: %+v", id1)
	}

	// Second call with a DIFFERENT timestamp must reuse — not regenerate.
	id2, err := EnsureIdentity(context.Background(), dir, gen, "2099-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("reuse: %v", err)
	}
	if gen.calls != 1 {
		t.Errorf("identity regenerated (calls=%d) — continuity broken", gen.calls)
	}
	if id2.Fingerprint != id1.Fingerprint || id2.CreatedAt != id1.CreatedAt {
		t.Errorf("identity changed across runs: %+v vs %+v", id2, id1)
	}
}

func TestEnsureIdentity_FatalOnFingerprintDrift(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureIdentity(context.Background(), dir, &fakeGen{}, "t0"); err != nil {
		t.Fatal(err)
	}
	// Tamper the public key so its fingerprint no longer matches the record.
	if err := os.WriteFile(filepath.Join(dir, pubFile), []byte("PUBKEY-TAMPERED"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := EnsureIdentity(context.Background(), dir, &fakeGen{}, "t1")
	if err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("expected fatal drift error, got: %v", err)
	}
}

func TestEnsureIdentity_FatalOnPartialState(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureIdentity(context.Background(), dir, &fakeGen{}, "t0"); err != nil {
		t.Fatal(err)
	}
	// Lose the private key — must NOT silently re-mint a new identity.
	if err := os.Remove(filepath.Join(dir, keyFile)); err != nil {
		t.Fatal(err)
	}
	gen := &fakeGen{}
	_, err := EnsureIdentity(context.Background(), dir, gen, "t1")
	if err == nil || !strings.Contains(err.Error(), "partial") {
		t.Fatalf("expected fatal partial-state error, got: %v", err)
	}
	if gen.calls != 0 {
		t.Errorf("regenerated over partial state (calls=%d)", gen.calls)
	}
}

func TestEnsureIdentity_FatalOnCorruptRecord(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, identityFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := EnsureIdentity(context.Background(), dir, &fakeGen{}, "t0")
	if err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("expected fatal corrupt-record error, got: %v", err)
	}
}

func TestEnsureIdentity_ReTightensKeyPerms(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureIdentity(context.Background(), dir, &fakeGen{}, "t0"); err != nil {
		t.Fatal(err)
	}
	kp := filepath.Join(dir, keyFile)
	if err := os.Chmod(kp, 0o644); err != nil { // simulate a perms drift (backup/restore)
		t.Fatal(err)
	}
	if _, err := EnsureIdentity(context.Background(), dir, &fakeGen{}, "t1"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(kp)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("private key not re-tightened to 0600 on reuse: %o", fi.Mode().Perm())
	}
}

// A CRLF/trailing-newline difference in the PEM must NOT change the fingerprint
// (it hashes the DER, not the file bytes) — otherwise a backup/checkout that
// touches cosign.pub would spuriously trip continuity and brick signing.
func TestFingerprint_EncodingInvariant(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.pub")
	b := filepath.Join(dir, "b.pub")
	c := filepath.Join(dir, "c.pub")
	// Same key material (base64 "aGVsbG8="), LF vs CRLF + extra trailing newline.
	mustWrite(t, a, "-----BEGIN PUBLIC KEY-----\naGVsbG8=\n-----END PUBLIC KEY-----\n")
	mustWrite(t, b, "-----BEGIN PUBLIC KEY-----\r\naGVsbG8=\r\n-----END PUBLIC KEY-----\r\n\n")
	mustWrite(t, c, "-----BEGIN PUBLIC KEY-----\nd29ybGQ=\n-----END PUBLIC KEY-----\n") // different material

	fa, err := fingerprint(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, _ := fingerprint(b)
	fc, _ := fingerprint(c)
	if fa != fb {
		t.Errorf("PEM encoding differences must not change the fingerprint: %s vs %s", fa, fb)
	}
	if fc == fa {
		t.Error("different key material must yield a different fingerprint")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGuardStateDir(t *testing.T) {
	repo := t.TempDir()
	if err := GuardStateDir(filepath.Join(repo, "sub", "signing"), repo); err == nil {
		t.Error("a state dir inside the repo must be refused (key could be committed/baked/published)")
	}
	if err := GuardStateDir(repo, repo); err == nil {
		t.Error("state dir == repo root must be refused")
	}
	if err := GuardStateDir(t.TempDir(), repo); err != nil {
		t.Errorf("a state dir outside the repo must be allowed: %v", err)
	}
}

func TestIsPrivateKeyPath(t *testing.T) {
	for _, p := range []string{
		"x/cosign.key", "identity.json", "/state/foo.key",
		"release.pem", "a.p12", "key.pfx", "id_rsa", "x/id_ed25519", "secret.asc", "k.gpg",
	} {
		if !IsPrivateKeyPath(p) {
			t.Errorf("%q should be flagged as key material", p)
		}
	}
	for _, p := range []string{"SHA256SUMS.sig", "cosign.pub", "x/archive.tar.gz", "notes.md"} {
		if IsPrivateKeyPath(p) {
			t.Errorf("%q must NOT be flagged", p)
		}
	}
}

// A symlink that resolves INTO the repo must be refused — the guard is symlink-safe.
func TestGuardStateDir_SymlinkIntoRepoRefused(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(repo, "signing")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(outside, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if err := GuardStateDir(link, repo); err == nil {
		t.Error("a state dir symlinked into the repo must be refused")
	}
}

func TestEnsureIdentity_FatalOnOrphanKeyMaterial(t *testing.T) {
	dir := t.TempDir()
	// Key material with no identity record (e.g. a half-restored backup) — refuse.
	if err := os.WriteFile(filepath.Join(dir, keyFile), []byte("PRIVKEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	gen := &fakeGen{}
	_, err := EnsureIdentity(context.Background(), dir, gen, "t0")
	if err == nil || !strings.Contains(err.Error(), "no identity record") {
		t.Fatalf("expected fatal orphan-material error, got: %v", err)
	}
	if gen.calls != 0 {
		t.Errorf("provisioned over orphan material (calls=%d)", gen.calls)
	}
}
