package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ExtractPredicate must pull the predicate BODY out of a full in-toto statement
// (so cosign attest re-frames it with the image subject, never double-wrapping the
// whole statement), and report the canonical statement's sha as the provenance
// identity. Absent / unreadable / predicate-less statements report ok=false so an
// enabled attestation fails loud rather than attaching nothing.
func TestExtractPredicate(t *testing.T) {
	dir := t.TempDir()

	t.Run("extracts predicate body and records statement sha", func(t *testing.T) {
		stmt := filepath.Join(dir, "docker-abc.json")
		body := `{"_type":"https://in-toto.io/Statement/v1","predicateType":"https://slsa.dev/provenance/v1","subject":[{"name":"app"}],"predicate":{"buildType":"x","builder":{"id":"y"}}}`
		if err := os.WriteFile(stmt, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		predPath, sha, ok := ExtractPredicate(stmt)
		if !ok {
			t.Fatal("expected ok for a well-formed statement")
		}
		if !strings.HasPrefix(sha, "sha256:") {
			t.Fatalf("statement sha not recorded: %q", sha)
		}
		pb, err := os.ReadFile(predPath)
		if err != nil {
			t.Fatalf("predicate file not written: %v", err)
		}
		s := string(pb)
		if !strings.Contains(s, `"buildType":"x"`) {
			t.Fatalf("predicate body missing buildType: %s", s)
		}
		// The written file is the predicate BODY only — no in-toto framing.
		if strings.Contains(s, "_type") || strings.Contains(s, "subject") {
			t.Fatalf("predicate must not carry statement framing: %s", s)
		}
	})

	t.Run("absent / empty path -> not ok", func(t *testing.T) {
		if _, _, ok := ExtractPredicate(filepath.Join(dir, "nope.json")); ok {
			t.Fatal("absent statement must report not-ok")
		}
		if _, _, ok := ExtractPredicate(""); ok {
			t.Fatal("empty path must report not-ok")
		}
	})

	t.Run("statement without predicate -> not ok but sha recorded", func(t *testing.T) {
		stmt := filepath.Join(dir, "no-pred.json")
		if err := os.WriteFile(stmt, []byte(`{"_type":"x","subject":[]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		_, sha, ok := ExtractPredicate(stmt)
		if ok {
			t.Fatal("a predicate-less statement must report not-ok")
		}
		if sha == "" {
			t.Fatal("sha of a readable statement should still be recorded")
		}
	})
}

// FindBuildProvenance returns statements but never the .predicate.json bodies, and
// returns nil (not an error) when the provenance dir does not exist.
func TestFindBuildProvenance(t *testing.T) {
	root := t.TempDir()
	if got, err := FindBuildProvenance(root); err != nil || got != nil {
		t.Fatalf("missing provenance dir must be (nil,nil), got (%v,%v)", got, err)
	}
	dir := filepath.Join(root, ProvenanceDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"docker-abc.json", "docker-abc.json.predicate.json", "crucible-xyz.json"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := FindBuildProvenance(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 statements (predicate body excluded), got %d: %v", len(got), got)
	}
	for _, p := range got {
		if strings.HasSuffix(p, ".predicate.json") {
			t.Fatalf("predicate body must be excluded: %s", p)
		}
	}
}
