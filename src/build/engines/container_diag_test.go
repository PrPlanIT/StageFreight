package engines

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestStderrTail_SurfacesStderr locks the diagnostic fix: a failed `docker create`
// previously collapsed to a bare "exit status 1". Cmd.Output() captures stderr into
// ExitError.Stderr, and stderrTail must recover it so the real reason (here, the
// classic disk-exhaustion message) reaches the user.
func TestStderrTail_SurfacesStderr(t *testing.T) {
	_, err := exec.Command("sh", "-c", "echo 'no space left on device' >&2; exit 1").Output()
	if err == nil {
		t.Fatal("expected the command to fail")
	}
	if tail := stderrTail(err); !strings.Contains(tail, "no space left on device") {
		t.Errorf("stderrTail = %q, want it to include the real stderr", tail)
	}
}

func TestStderrTail_Empty(t *testing.T) {
	if got := stderrTail(nil); got != "" {
		t.Errorf("stderrTail(nil) = %q, want empty", got)
	}
	if got := stderrTail(errors.New("plain error, no captured stderr")); got != "" {
		t.Errorf("stderrTail(plain) = %q, want empty", got)
	}
}

func TestFreeDiskGiB(t *testing.T) {
	if got := freeDiskGiB("."); got < 0 {
		t.Errorf("freeDiskGiB(.) = %v, want >= 0 on a real filesystem", got)
	}
	if got := freeDiskGiB("/no/such/path/really/nope"); got != -1 {
		t.Errorf("freeDiskGiB(bogus) = %v, want -1", got)
	}
}
