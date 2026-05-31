package cmd

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestNoIdentityReconstructionPatterns is a TRIPWIRE for the Phase 4B
// reconstruction lint rule via static source inspection of release_create.go.
//
// Per the locked invariant: ArtifactID is the only join key in
// release_create. Any reconstruction of identity from fields (name + os +
// arch, BuildID/OS/Arch composite keys, name parsing) is a correctness
// violation, not a convenience shortcut.
//
// IMPORTANT LIMITATION: this is pattern matching, not absence-proof. It
// catches the well-known regression shapes but it CANNOT prove no
// reconstruction exists. A motivated regression like
//   `key := strings.Join(parts, "/")`
// or
//   `type artifactKey struct { name, os, arch string }`
// or
//   `map[artifactCoord]X`
// would slip past. The real protection is the typed `artifact.ArtifactID`
// at API boundaries (compile-time enforcement) — this test is the cheap
// extra layer that catches the obvious mistakes at PR time.
func TestNoIdentityReconstructionPatterns(t *testing.T) {
	srcBytes, err := os.ReadFile("release_create.go")
	if err != nil {
		t.Fatalf("read release_create.go: %v", err)
	}
	src := string(srcBytes)

	// Each pattern is checked as a regex against the source. The intent
	// is hostile-reading — if the pattern appears, the reviewer is asked
	// to justify it (or replace it). Allowed exceptions belong in the
	// "presentation-only" set documented at the top of release_create.go.
	type rule struct {
		name        string
		pattern     *regexp.Regexp
		description string
	}
	rules := []rule{
		{
			name:        "BuildID-composite-key",
			pattern:     regexp.MustCompile(`BuildID\s*\+\s*"/"\s*\+\s*\w+\.OS`),
			description: "BuildID/OS/Arch composite key reconstructs identity from fields",
		},
		{
			name:        "name-os-arch-composite",
			pattern:     regexp.MustCompile(`\.Name\s*\+\s*"-"\s*\+\s*\w+\.OS\s*\+\s*"-"\s*\+\s*\w+\.Arch`),
			description: "name+os+arch string concat reconstructs binary identity",
		},
		{
			name:        "split-on-artifact-name",
			pattern:     regexp.MustCompile(`strings\.Split\(\s*\w+\.ArtifactName`),
			description: "Splitting ArtifactName parses identity from a presentation string",
		},
		{
			name:        "split-on-artifactid",
			pattern:     regexp.MustCompile(`strings\.Split\(\s*\w*[Ii]d\.\w*ArtifactID|strings\.Split\(string\(\w+\.ArtifactID\)`),
			description: "Splitting ArtifactID reverses the canonical encoding to get type/name",
		},
		{
			name:        "sprintf-binary-identity",
			pattern:     regexp.MustCompile(`fmt\.Sprintf\([^)]*"[^"]*-%s-%s"[^)]*OS[^)]*Arch`),
			description: "Sprintf composing binary identity from os/arch",
		},
		{
			name:        "hasprefix-binary-artifactid",
			pattern:     regexp.MustCompile(`strings\.HasPrefix\([^,]*,\s*"binary:"`),
			description: "Type heuristic on ArtifactID string — use typed comparison or view kind field",
		},
		{
			name:        "hasprefix-archive-artifactid",
			pattern:     regexp.MustCompile(`strings\.HasPrefix\([^,]*,\s*"archive:"`),
			description: "Type heuristic on ArtifactID string — use typed comparison or view kind field",
		},
	}

	for _, r := range rules {
		if loc := r.pattern.FindStringIndex(src); loc != nil {
			line := 1 + strings.Count(src[:loc[0]], "\n")
			snippet := src[loc[0]:loc[1]]
			t.Errorf("identity reconstruction pattern %q detected at line %d: %q\n  %s",
				r.name, line, snippet, r.description)
		}
	}
}

// TestAllJoinsUseArtifactID asserts that join maps inside release_create
// are keyed by artifact.ArtifactID, not bare string, when the key
// represents artifact identity.
//
// Per the secondary watch item: if a value participates in lookup,
// membership, correlation, deduplication, or joining, the type should
// remain ArtifactID. Only convert to string at presentation boundaries.
func TestAllJoinsUseArtifactID(t *testing.T) {
	srcBytes, err := os.ReadFile("release_create.go")
	if err != nil {
		t.Fatalf("read release_create.go: %v", err)
	}
	src := string(srcBytes)

	// Patterns that indicate a join/lookup keyed by bare string against
	// what should be an artifact identity. These match map declarations
	// where the key is `string` and the surrounding context suggests
	// artifact-identity semantics (covered, binaryByID, archiveByID, etc).
	identityJoinPatterns := []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{
			name:    "covered-string-key",
			pattern: regexp.MustCompile(`covered\w*\s*:?=\s*make\(map\[string\]`),
		},
		{
			name:    "binaryByID-string-key",
			pattern: regexp.MustCompile(`binaryByID\s*:?=\s*make\(map\[string\]`),
		},
		{
			name:    "archiveByID-string-key",
			pattern: regexp.MustCompile(`archiveByID\s*:?=\s*make\(map\[string\]`),
		},
	}
	for _, p := range identityJoinPatterns {
		if loc := p.pattern.FindStringIndex(src); loc != nil {
			line := 1 + strings.Count(src[:loc[0]], "\n")
			snippet := src[loc[0]:loc[1]]
			t.Errorf("identity join keyed by string %q at line %d: %q\n  Should be map[artifact.ArtifactID]X — bare string keys silently bypass typed-identity safety",
				p.name, line, snippet)
		}
	}
}
