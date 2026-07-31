package narrate

import (
	"strings"
	"testing"
)

// fixtureInput is a representative passing image-modality run.
func fixtureInput() Input {
	return Input{
		Project:     "stagefreight",
		Description: "GitOps-native CI/CD for the paranoid",
		Modality:    "image",
		Status:      StatusPassing,
		Ref:         "main",
		SHA:         "cab9d0f1",
		Version:     "0.7.0-dev+086d087",
		PipelineURL: "https://gitlab.example/37/-/pipelines/1234",
		Phases: []Phase{
			{Name: "perform", Outcome: "success"},
			{Name: "review", Outcome: "success"},
			{Name: "publish", Outcome: "success"},
		},
		Changes: []ChangeGroup{
			{Title: "Features", Entries: []ChangeEntry{
				{Scope: "release", Summary: "cross-forge release mirroring"},
				{Scope: "scribe", Summary: "perform-time compose"},
			}},
			{Title: "Bug Fixes", Entries: []ChangeEntry{
				{Scope: "badge", Summary: "reproducible subset"},
			}},
		},
		ChangeCount:  12,
		SinceRef:     "dev-b7a7f50",
		Published:    6,
		Registries:   []string{"docker.io", "ghcr.io", "cr.pcfae.com"},
		ReleaseTag:   "dev-cab9d0f",
		ReleaseURL:   "https://gitlab.example/37/-/releases/dev-cab9d0f",
		Housekeeping: []string{"retention — pruned `dev-0c73426` (3 registries)", "mirror — github ✓"},
	}
}

// The headline invariant: same Input renders byte-identical Markdown. Same
// reproducibility discipline the badge subset enforces — a summary that churned
// every run would be worthless as a stored "last summary." (Only the pure,
// deterministic core is covered; an ollama projection is explicitly outside it.)
func TestRenderStory_Deterministic(t *testing.T) {
	in := fixtureInput()
	a := RenderStory(in)
	b := RenderStory(in)
	if a != b {
		t.Fatalf("RenderStory not deterministic:\n---a---\n%s\n---b---\n%s", a, b)
	}
}

func TestRenderStory_PassingShape(t *testing.T) {
	got := RenderStory(fixtureInput())
	for _, want := range []string{
		"# stagefreight — passed",
		"**image** · main · `cab9d0f1` · 0.7.0-dev+086d087 · [pipeline](https://gitlab.example/37/-/pipelines/1234)",
		"Passed — shipped `dev-cab9d0f` to 6 images.",
		"## Changes _(12 commits since `dev-b7a7f50`)_",
		"**Features**",
		"- **release:** cross-forge release mirroring",
		"## Phases",
		"perform ✓ · review ✓ · publish ✓",
		"- **images** — 6 images published across docker.io · ghcr.io · cr.pcfae.com",
		"- **release** — [`dev-cab9d0f`](https://gitlab.example/37/-/releases/dev-cab9d0f)",
		"## Housekeeping",
		"- mirror — github ✓",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("passing story missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "## What broke") {
		t.Fatalf("passing story must not show the failure branch:\n%s", got)
	}
}

// On failure the story inverts to the post-mortem branch, apex-first with the
// failing subsystem + reason.
func TestRenderStory_FailingInverts(t *testing.T) {
	in := fixtureInput()
	in.Status = StatusFailing
	in.Phases = []Phase{
		{Name: "perform", Outcome: "success"},
		{Name: "review", Outcome: "success"},
		{Name: "publish", Outcome: "failed", Reason: "promotion denied: pipeline SHA is not branch HEAD", Blocking: true},
	}
	got := RenderStory(in)
	for _, want := range []string{
		"# stagefreight — failed",
		"Failed at **publish** — promotion denied: pipeline SHA is not branch HEAD",
		"## What broke",
		"- **publish** — promotion denied: pipeline SHA is not branch HEAD",
		"## Changes", // changelog still shows on failure (what it *would* have shipped)
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("failing story missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "## Phases") {
		t.Fatalf("failing story must invert away from the Phases branch:\n%s", got)
	}
}

// Missing data degrades gracefully — no broken stencil, optional sections drop.
func TestRenderStory_GracefulEmpty(t *testing.T) {
	got := RenderStory(Input{
		Project:  "bare",
		Modality: "image",
		Status:   StatusPassing,
		Ref:      "main",
		SHA:      "0000000",
		Phases:   []Phase{{Name: "perform", Outcome: "success"}},
	})
	for _, want := range []string{
		"# bare — passed",
		"_No code changes since the last release._",
		"_Nothing distributable shipped this run._",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("bare story missing %q:\n%s", want, got)
		}
	}
	for _, absent := range []string{"## Housekeeping", "[pipeline]"} {
		if strings.Contains(got, absent) {
			t.Fatalf("bare story should not contain %q:\n%s", absent, got)
		}
	}
}
