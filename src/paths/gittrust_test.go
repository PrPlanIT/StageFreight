package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitTrustEnvOutsideCI(t *testing.T) {
	t.Setenv("SF_CI_WORKSPACE", "")
	if env := GitTrustEnv(); env != nil {
		t.Fatalf("trusted a workspace outside CI: %v", env)
	}
}

// The real failure: git refuses a checkout owned by another user, and the trust env makes
// it read it. Only runnable as root, where the checkout can be handed to another owner.
func TestGitTrustEnvLetsGitReadAForeignCheckout(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root to give the checkout another owner")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "x"}} {
		if out, err := exec.Command(git, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := filepath.Walk(repo, func(p string, _ os.FileInfo, _ error) error { return os.Lchown(p, 4242, 4242) }); err != nil {
		t.Fatal(err)
	}
	revParse := func(extra []string) (string, error) {
		cmd := exec.Command(git, "-C", repo, "rev-parse", "HEAD")
		cmd.Env = append([]string{"HOME=" + dir, "PATH=" + os.Getenv("PATH")}, extra...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := revParse(nil); err == nil || !strings.Contains(out, "dubious ownership") {
		t.Fatalf("expected dubious ownership before trusting, got err=%v %s", err, out)
	}
	t.Setenv("SF_CI_WORKSPACE", repo)
	if out, err := revParse(GitTrustEnv()); err != nil {
		t.Fatalf("after trusting: %v %s", err, out)
	}
}
