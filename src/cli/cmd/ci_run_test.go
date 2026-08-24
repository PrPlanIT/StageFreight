package cmd

import "testing"

// generatedCommitShouldSkip is the loop-prevention backstop: on an AUTOMATIC push a narrate
// commit (Generated-By) self-skips, but an intentional trigger (web/api/schedule) runs even
// on a narrate HEAD — so a maintainer can rebuild against an updated engine image. Tags and
// deps (Updated-By) commits always build.
func TestGeneratedCommitShouldSkip(t *testing.T) {
	const narrate = "docs: refresh generated assets\n\nGenerated-By: StageFreight"
	cases := []struct {
		name    string
		event   string
		isTag   bool
		message string
		want    bool
	}{
		{"narrate push skips (the loop)", "push", false, narrate, true},
		{"deps push builds", "push", false, "fix(deps): bump x\n\nUpdated-By: StageFreight", false},
		{"human push builds", "push", false, "feat: a real feature", false},
		{"narrate tip on a tag still builds", "push", true, narrate, false},
		{"empty message builds (read failed → fail open)", "push", false, "", false},
		// Intentional triggers must run even on a narrate HEAD — the loop is push-only.
		{"narrate web (manual) trigger builds", "web", false, narrate, false},
		{"narrate schedule builds", "schedule", false, narrate, false},
		{"narrate api trigger builds", "api", false, narrate, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := generatedCommitShouldSkip(tc.event, tc.isTag, tc.message); got != tc.want {
				t.Errorf("generatedCommitShouldSkip(%q, %v, %q) = %v, want %v", tc.event, tc.isTag, tc.message, got, tc.want)
			}
		})
	}
}
