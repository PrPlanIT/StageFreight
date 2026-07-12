package cmd

import "testing"

// generatedCommitShouldSkip is the uniform loop-prevention backstop: narrate commits
// (Generated-By) self-skip every phase on any forge and locally, while tags and deps
// (Updated-By) commits build.
func TestGeneratedCommitShouldSkip(t *testing.T) {
	cases := []struct {
		name    string
		isTag   bool
		message string
		want    bool
	}{
		{"narrate branch commit skips", false, "docs: refresh generated assets\n\nGenerated-By: StageFreight", true},
		{"deps branch commit builds", false, "fix(deps): bump x\n\nUpdated-By: StageFreight", false},
		{"human commit builds", false, "feat: a real feature", false},
		{"narrate tip on a tag still builds", true, "docs: refresh generated assets\n\nGenerated-By: StageFreight", false},
		{"empty message builds (read failed → fail open)", false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := generatedCommitShouldSkip(tc.isTag, tc.message); got != tc.want {
				t.Errorf("generatedCommitShouldSkip(%v, %q) = %v, want %v", tc.isTag, tc.message, got, tc.want)
			}
		})
	}
}
