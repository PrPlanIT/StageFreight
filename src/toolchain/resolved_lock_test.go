package toolchain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const amd64, arm64 = "linux/amd64", "linux/arm64"

func TestLock_ReadMissingIsEmpty(t *testing.T) {
	l, err := ReadLock(t.TempDir())
	if err != nil {
		t.Fatalf("missing lock must not error: %v", err)
	}
	if len(l.Toolchains) != 0 || l.LockfileVersion != LockfileVersion {
		t.Errorf("missing lock should be empty at current version, got %+v", l)
	}
}

func TestLock_SetGetResolved(t *testing.T) {
	l := &Lock{}
	if !l.Set("trivy", "0.69.3", amd64, "abc") {
		t.Error("first Set must report a change")
	}
	if l.Set("trivy", "0.69.3", amd64, "abc") {
		t.Error("identical Set must be a no-op")
	}
	if !l.Set("trivy", "0.69.4", amd64, "def") {
		t.Error("changed Set must report a change")
	}
	if !l.Set("kubectl", "1.26.7", amd64, "") {
		t.Error("new tool Set must report a change")
	}
	if l.Set("x", "", amd64, "y") {
		t.Error("empty resolved must be rejected")
	}
	if got := l.Resolved("trivy"); got != "0.69.4" {
		t.Errorf("Resolved(trivy) = %q, want 0.69.4", got)
	}
	if got := l.Resolved("absent"); got != "" {
		t.Errorf("Resolved(absent) = %q, want empty", got)
	}
	// Entries are kept sorted by name (kubectl before trivy).
	if l.Toolchains[0].Name != "kubectl" || l.Toolchains[1].Name != "trivy" {
		t.Errorf("entries not sorted: %+v", l.Toolchains)
	}
}

// TestLock_DigestPerPlatform: digests are kept per os/arch for the locked version; a new
// version drops the previous version's digests rather than carrying them over.
func TestLock_DigestPerPlatform(t *testing.T) {
	l := &Lock{}
	l.Set("trivy", "0.76.1", amd64, "aaa")
	if !l.Set("trivy", "0.76.1", arm64, "bbb") {
		t.Fatal("a second platform's digest must report a change")
	}
	if got := l.Digest("trivy", "0.76.1", amd64); got != "aaa" {
		t.Errorf("amd64 digest = %q, want aaa", got)
	}
	if got := l.Digest("trivy", "0.76.1", arm64); got != "bbb" {
		t.Errorf("arm64 digest = %q, want bbb", got)
	}
	if got := l.Digest("trivy", "0.69.3", amd64); got != "" {
		t.Errorf("a digest must not answer for another version, got %q", got)
	}
	if got := l.Digest("trivy", "0.76.1", "darwin/arm64"); got != "" {
		t.Errorf("an unrecorded platform has no digest, got %q", got)
	}

	l.Set("trivy", "0.77.0", amd64, "ccc")
	if got := l.Digest("trivy", "0.77.0", arm64); got != "" {
		t.Errorf("a new version must drop the old version's arm64 digest, got %q", got)
	}
	if got := l.Digest("trivy", "0.77.0", amd64); got != "ccc" {
		t.Errorf("new version amd64 digest = %q, want ccc", got)
	}
}

func TestLock_WriteReadRoundtripDeterministic(t *testing.T) {
	root := t.TempDir()
	l := &Lock{}
	l.Set("trivy", "0.69.3", amd64, "abc")
	l.Set("kubectl", "1.26.7", amd64, "def")
	l.Set("kubectl", "1.26.7", arm64, "ghi")
	if err := WriteLock(root, l); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	// Deterministic: a second write of the same content is byte-identical.
	first, _ := os.ReadFile(LockPath(root))
	if err := WriteLock(root, l); err != nil {
		t.Fatalf("WriteLock 2: %v", err)
	}
	second, _ := os.ReadFile(LockPath(root))
	if string(first) != string(second) {
		t.Error("lock serialization is not deterministic")
	}
	back, err := ReadLock(root)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if back.Digest("kubectl", "1.26.7", amd64) != "def" || back.Digest("kubectl", "1.26.7", arm64) != "ghi" {
		t.Errorf("roundtrip lost kubectl digests: %+v", back.Toolchains)
	}
}

// TestLock_OldVersionRejected: a lock from another schema version fails with a hint to
// regenerate it, never a bare JSON error.
func TestLock_OldVersionRejected(t *testing.T) {
	root := t.TempDir()
	path := LockPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	v1 := `{"lockfileVersion": 1, "toolchains": [{"name": "trivy", "resolved": "0.69.3", "sha256": "abc"}]}`
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadLock(root)
	if err == nil || !strings.Contains(err.Error(), "lockfileVersion 1") || !strings.Contains(err.Error(), "delete it") {
		t.Errorf("v1 lock must be rejected with a regenerate hint, got %v", err)
	}
}
