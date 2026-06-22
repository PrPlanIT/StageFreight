package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PrPlanIT/StageFreight/src/version"
)

const (
	updateDefaultImage = "docker.io/prplanit/stagefreight:latest"
	updateDevImage     = "docker.io/prplanit/stagefreight:latest-dev"
	imageBinaryPath    = "/usr/local/bin/stagefreight"
)

var (
	updateImage string
	updateDev   bool
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update this stagefreight binary in place from the published image",
	Long: "Pull the StageFreight image and atomically replace the running binary with the one " +
		"inside it.\n\n" +
		"  stagefreight update          " + updateDefaultImage + "\n" +
		"  stagefreight update --dev    " + updateDevImage + "\n" +
		"  stagefreight update --image <ref>\n\n" +
		"The image binary is static (CGO_ENABLED=0) so it runs on any linux host; it is verified " +
		"to run here before the swap, and the swap is atomic — the running process is unaffected.",
	RunE: runUpdate,
}

func init() {
	updateCmd.Flags().StringVar(&updateImage, "image", "", "image ref to update from (overrides default and --dev)")
	updateCmd.Flags().BoolVar(&updateDev, "dev", false, "update from the latest-dev image instead of the latest release")
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(cmd *cobra.Command, args []string) error {
	ref := updateImage
	if ref == "" {
		ref = updateDefaultImage
		if updateDev {
			ref = updateDevImage
		}
	}

	// Resolve where we're installed (follow symlinks so we replace the real file).
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating this binary: %w", err)
	}
	if resolved, lerr := filepath.EvalSymlinks(self); lerr == nil {
		self = resolved
	}
	dir := filepath.Dir(self)

	// The staged binary must land in the SAME directory (atomic rename needs the
	// same filesystem), and being able to create it there proves we can replace
	// the target.
	staged := filepath.Join(dir, ".stagefreight.update")
	if f, ferr := os.OpenFile(staged, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755); ferr != nil {
		return fmt.Errorf("%s is not writable — rerun with sudo, or reinstall to a writable dir like ~/.local/bin: %w", dir, ferr)
	} else {
		f.Close()
	}
	defer os.Remove(staged)

	fmt.Fprintf(os.Stdout, "→ pulling %s\n", ref)
	if out, perr := runDocker("pull", ref); perr != nil {
		return fmt.Errorf("docker pull %s: %w\n%s", ref, perr, strings.TrimSpace(out))
	}

	// Extract the binary from a (never-started) container of the image.
	cidOut, cerr := runDocker("create", ref)
	if cerr != nil {
		return fmt.Errorf("docker create: %w\n%s", cerr, strings.TrimSpace(cidOut))
	}
	cid := strings.TrimSpace(cidOut)
	defer runDocker("rm", "-f", cid)
	if out, cperr := runDocker("cp", cid+":"+imageBinaryPath, staged); cperr != nil {
		return fmt.Errorf("extracting %s from image: %w\n%s", imageBinaryPath, cperr, strings.TrimSpace(out))
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		return err
	}

	// Verify the new binary actually runs HERE before trusting it (guards against
	// an arch/libc mismatch bricking the install).
	newVer, verr := exec.Command(staged, "version").Output()
	if verr != nil {
		return fmt.Errorf("the extracted binary did not run on this host (arch/libc mismatch?): %w", verr)
	}

	// Atomic swap: the running process keeps its old inode; new invocations get
	// the replacement.
	if err := os.Rename(staged, self); err != nil {
		return fmt.Errorf("replacing %s: %w", self, err)
	}

	fmt.Fprintf(os.Stdout, "✓ updated %s\n  was: %s\n  now: %s\n",
		self, strings.TrimSpace(version.String()), strings.TrimSpace(string(newVer)))
	return nil
}

func runDocker(args ...string) (string, error) {
	out, err := exec.Command("docker", args...).CombinedOutput()
	return string(out), err
}
