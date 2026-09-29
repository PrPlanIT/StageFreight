package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/workspace"
)

// adoptionsDir is where pre-adoption release bodies are preserved. It lives under
// the SF workspace namespace, which every repo's self-contained .stagefreight/
// .gitignore denies by default — so the artifact NEVER enters the repo — while
// still resolving under $CI_PROJECT_DIR, which is the only place a GitLab job can
// collect an artifact from.
const adoptionsSubdir = "release-adoptions"

var unsafeTagChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// preservedRelease is the on-disk record of a release as it stood immediately
// before adoption overwrote it.
type preservedRelease struct {
	CapturedAt time.Time `json:"captured_at"`
	Mirror     string    `json:"mirror"`
	Tag        string    `json:"tag"`
	ReleaseID  string    `json:"release_id"`
	Name       string    `json:"name"`
	Prerelease bool      `json:"prerelease"`
	Body       string    `json:"body"`
}

// adoptionPreserver builds the mirror.Options.PreserveAdopted hook: it writes the
// mirror's current release to a recoverable artifact before adoption replaces it.
//
// Adoption is the one step that overwrites content StageFreight did not write, so
// it is the one step that can lose something. If the artifact cannot be written the
// body is emitted to the job log instead — logs outlive the workspace, so the
// content is never gone without a trace. Only if BOTH fail does the hook error,
// which aborts that adoption rather than destroying an unpreserved release.
func adoptionPreserver(rootDir, mirrorID string, log io.Writer) func(string, forge.ReleaseInfo) error {
	return func(tag string, prior forge.ReleaseInfo) error {
		rec := preservedRelease{
			CapturedAt: time.Now().UTC(),
			Mirror:     mirrorID,
			Tag:        tag,
			ReleaseID:  prior.ID,
			Name:       prior.Name,
			Prerelease: prior.Prerelease,
			Body:       prior.Description,
		}
		blob, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding prior release: %w", err)
		}

		dir := filepath.Join(rootDir, workspace.NamespaceDir, adoptionsSubdir)
		name := fmt.Sprintf("%s-%s-%d.json", unsafeTagChars.ReplaceAllString(mirrorID, "_"),
			unsafeTagChars.ReplaceAllString(tag, "_"), rec.CapturedAt.UnixNano())
		path := filepath.Join(dir, name)

		writeErr := os.MkdirAll(dir, 0o755)
		if writeErr == nil {
			writeErr = os.WriteFile(path, blob, 0o644)
		}
		if writeErr == nil {
			fmt.Fprintf(log, "  adopt %s: prior release preserved → %s\n",
				tag, filepath.Join(workspace.NamespaceDir, adoptionsSubdir, name))
			return nil
		}

		// Artifact unavailable — fall back to the job log, which outlives the workspace.
		if _, logErr := fmt.Fprintf(log,
			"  adopt %s: could not write %s (%v) — preserving prior release inline instead:\n%s\n",
			tag, path, writeErr, blob); logErr != nil {
			return fmt.Errorf("could not write artifact (%v) nor log it (%w)", writeErr, logErr)
		}
		return nil
	}
}
