package toolchain

import (
	"os"
	"testing"
)

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
	if !l.Set("trivy", "0.69.3", "abc") {
		t.Error("first Set must report a change")
	}
	if l.Set("trivy", "0.69.3", "abc") {
		t.Error("identical Set must be a no-op")
	}
	if !l.Set("trivy", "0.69.4", "def") {
		t.Error("changed Set must report a change")
	}
	if !l.Set("kubectl", "1.26.7", "") {
		t.Error("new tool Set must report a change")
	}
	if l.Set("x", "", "y") {
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

func TestLock_WriteReadRoundtripDeterministic(t *testing.T) {
	root := t.TempDir()
	l := &Lock{}
	l.Set("trivy", "0.69.3", "abc")
	l.Set("kubectl", "1.26.7", "def")
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
	if e, ok := back.Get("kubectl"); !ok || e.Resolved != "1.26.7" || e.SHA256 != "def" {
		t.Errorf("roundtrip lost kubectl entry: %+v", back.Toolchains)
	}
}
