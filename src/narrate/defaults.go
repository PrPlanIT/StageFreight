package narrate

import _ "embed"

// imageDefaultTemplate is SF's embedded, forkable default story for the image
// (build-and-ship) modality — the detailed default, the SOURCE of truth. It is
// complete (a reader knows everything within reason) and dual-legible (reads well
// raw AND parses cleanly for an AI to summarize). Terser, purpose-shaped outputs
// are projections of THIS, never competing defaults.
//
// Kept as a .md file (not a Go string) so it doubles as the scaffold source for
// the future `sf narrate template <modality>` command — a user forks the real
// thing, then makes it their own.
//
//go:embed templates/image.md
var imageDefaultTemplate string

// defaultTemplate returns the embedded default story for a lifecycle modality.
// Skeleton: image/docker are wired; gitops/governance fall back to image until
// their own defaults land (build order step 7). A run always has a story.
func defaultTemplate(modality string) string {
	switch modality {
	case "image", "docker":
		return imageDefaultTemplate
	default:
		return imageDefaultTemplate
	}
}
