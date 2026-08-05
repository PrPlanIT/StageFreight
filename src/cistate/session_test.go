package cistate

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSessionLedger pins the session/state split: RecordSubsystem stamps the
// session (this process's work); overlaying forwarded shards upserts state
// WITHOUT stamping — loaded records must never masquerade as executed ones.
func TestSessionLedger(t *testing.T) {
	sessionMu.Lock()
	sessionSubsystems = nil
	sessionMu.Unlock()

	dir := t.TempDir()
	shardDir := filepath.Join(dir, SubsystemsDir)
	if err := os.MkdirAll(shardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shardDir, "build.json"),
		[]byte(`{"name":"build","attempted":true,"completed":true,"outcome":"success"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	st := &State{}
	overlaySubsystemShards(dir, st)
	if len(st.Subsystems) != 1 || st.Subsystems[0].Name != "build" {
		t.Fatalf("overlay must upsert state: %+v", st.Subsystems)
	}
	if got := SessionSubsystems(); len(got) != 0 {
		t.Fatalf("overlay must NOT stamp the session: %+v", got)
	}

	st.RecordSubsystem(SubsystemState{Name: "ansible", Outcome: "failed", Required: true})
	got := SessionSubsystems()
	if len(got) != 1 || got[0].Name != "ansible" {
		t.Fatalf("RecordSubsystem must stamp the session: %+v", got)
	}
}
