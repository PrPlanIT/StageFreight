package release

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/release/trustdisclosure"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files")

// parityFullInput exercises every section of the release body: hero + metadata,
// security tile, image availability (with and without supply-chain extras),
// downloads, verification (anchored key signing), highlights, notable changes
// (including the ×N dedup path), the security section, and the full changelog.
func parityFullInput() (NotesInput, []CommitCategory, []Commit) {
	input := NotesInput{
		ProjectName:  "stagefreight",
		Version:      "0.7.0",
		SHA:          "abcd1234",
		ReleaseType:  "latest",
		SecurityTile: "🛡️ ✅ **Passed** — no vulnerabilities",
		SecurityBody: "No blocking vulnerabilities.\n\n<details>\n<summary>Scan detail</summary>\n\n- CVE-2026-0001 (low)\n</details>",
		TagMessage:   "Ships the stencil engine\nAlso fixes badges",
		Images: []ImageRow{
			{
				RegistryLabel: "Docker Hub",
				RegistryURL:   "https://hub.docker.com/r/prplanit/stagefreight",
				ImageRef:      "docker.io/prplanit/stagefreight",
				Tags: []ResolvedTag{
					{Name: "v0.7.0", URL: "https://hub.docker.com/r/prplanit/stagefreight/tags?name=v0.7.0"},
					{Name: "latest"},
				},
				DigestRef: "docker.io/prplanit/stagefreight@sha256:75225fc",
				SBOM:      "stagefreight-0.7.0.sbom.spdx.json",
				Signature: "stagefreight-0.7.0.sig",
			},
			{
				RegistryLabel: "Harbor",
				ImageRef:      "cr.pcfae.com/prplanit/stagefreight",
				Tags:          []ResolvedTag{{Name: "v0.7.0"}},
			},
		},
		Downloads: []BinaryRow{
			{Platform: "linux/amd64", Name: "stagefreight-0.7.0-linux-amd64.tar.gz", Size: 34567890, SHA256: "aaaabbbbccccddddeeeeffff00001111222233334444555566667777888899990"},
			{Platform: "linux/arm64", Name: "stagefreight-0.7.0-linux-arm64.tar.gz", Size: 31234567, SHA256: "1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff"},
		},
		Verify: &trustdisclosure.Disclosure{
			Primary: &trustdisclosure.SignatureFact{
				Class: "key", Tier: "tier0-software", TrustDomain: "prplanit",
				Transparency: false, NonExportable: true, PhysicalPresence: true,
				Asset: "SHA256SUMS.sig", IsBlob: true,
			},
			Anchor: &trustdisclosure.Anchor{Fingerprint: "SHA256:abcdef012345", Asset: "cosign.pub"},
		},
	}
	commits := []Commit{
		{Hash: "abc1234", Type: "feat", Scope: "stencils", Summary: "shared text-composition library", Author: "kai", Breaking: true},
		{Hash: "def5678", Type: "fix", Scope: "narrate", Summary: "line elision", Author: "kai"},
		{Hash: "aaa1111", Type: "fix", Scope: "narrate", Summary: "line elision", Author: "kai"}, // dedup ×2
		{Hash: "bbb2222", Type: "docs", Summary: "refresh generated docs {and} badges", Author: ""},
	}
	categories := categorize(commits)
	return input, categories, commits
}

// parityMinimalInput is the all-optionals-absent case: hero, rule, empty changelog.
func parityMinimalInput() (NotesInput, []CommitCategory, []Commit) {
	return NotesInput{}, nil, nil
}

// TestRenderNotes_OverrideBody covers the stylable half: a user release-notes
// stencil reorders/drops sections, authors its own markdown around them, and
// unknown tokens stay visibly literal.
func TestRenderNotes_OverrideBody(t *testing.T) {
	input, cats, commits := parityFullInput()
	input.Config = &config.Config{Version: 1, Stencils: config.OrderedStencils{{
		ID: "release-notes", Type: "text",
		Body: "Thanks for flying PrPlanIT ✈\n\n{release.hero}\n{release.changes}\ninline hero-less footer {release.nope}",
	}}}

	got := renderNotes(input, cats, commits)
	if !strings.HasPrefix(got, "Thanks for flying PrPlanIT ✈\n") {
		t.Errorf("authored lead line missing:\n%q", got)
	}
	if !strings.Contains(got, "## Notable Changes") || !strings.Contains(got, "## 📦 stagefreight") {
		t.Errorf("selected sections missing:\n%q", got)
	}
	for _, dropped := range []string{"## Downloads", "## Image Availability", "## Verification", "Full changelog"} {
		if strings.Contains(got, dropped) {
			t.Errorf("dropped section %q still present", dropped)
		}
	}
	if !strings.Contains(got, "inline hero-less footer {release.nope}") {
		t.Errorf("unknown token must stay literal:\n%q", got)
	}
}

// TestComposeNotesBody pins the block-verbatim placement rule: a block line
// places element bytes verbatim, an empty block leaves nothing (not even the
// line), and inline substitution trims trailing newlines.
func TestComposeNotesBody(t *testing.T) {
	elements := map[string]string{
		"release.a": "A1\nA2\n\n",
		"release.b": "",
		"release.c": "C\n",
	}
	got := composeNotesBody("{release.a}\n{release.b}\nmid {release.c} line\n---", elements)
	want := "A1\nA2\n\nmid C line\n---"
	if got != want {
		t.Errorf("compose:\n got %q\nwant %q", got, want)
	}
}

// TestRenderNotes_GoldenParity is the byte-parity acceptance gate for the
// release-notes stencil migration: the rendered body must be BYTE-IDENTICAL to
// the pre-migration renderer's output (captured in testdata via -update before
// the refactor). Any composition change that shifts a single byte fails here.
func TestRenderNotes_GoldenParity(t *testing.T) {
	cases := []struct {
		name  string
		build func() (NotesInput, []CommitCategory, []Commit)
	}{
		{"full", parityFullInput},
		{"minimal", parityMinimalInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, cats, commits := tc.build()
			got := renderNotes(input, cats, commits)

			goldenPath := filepath.Join("testdata", "notes_"+tc.name+".golden.md")
			if *updateGolden {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("golden missing (run go test -update BEFORE refactoring): %v", err)
			}
			if got != string(want) {
				t.Errorf("release body is not byte-identical to the golden\n got: %q\nwant: %q", got, string(want))
			}
		})
	}
}
