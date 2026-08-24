package docker

import (
	"bytes"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/output"
)

// The command buildkit inlines in a process-failure line is collapsed; a short
// command is left alone.
func TestElideProcessCommand(t *testing.T) {
	long := strings.Repeat("curl -fsSL x && ", 100)
	line := `failed to solve: process "/bin/sh -c ` + long + `" did not complete successfully: exit code: 1`
	got := elideProcessCommand(line)
	if strings.Contains(got, long) {
		t.Fatal("long command was not elided")
	}
	if !strings.Contains(got, `process "…" did not complete`) {
		t.Fatalf("expected the elision marker, got: %.90q", got)
	}
	if !strings.Contains(got, "exit code: 1") {
		t.Error("the useful tail (exit code) was dropped")
	}

	short := `process "/bin/sh -c true" did not complete successfully: exit code: 1`
	if elideProcessCommand(short) != short {
		t.Error("a short command should be preserved verbatim")
	}
}

// A giant buildkit failure must render bounded, elided rows — and the actual
// signal (the curl 404) must survive. Regression for the whitespace explosion +
// ungraceful "dump the whole command" behavior.
func TestRenderBuildError_BoundedAndUseful(t *testing.T) {
	giant := strings.Repeat(`curl -fsSL \"https://example/x\" -o /y && `, 60)
	stderr := strings.Join([]string{
		`#7 12.3 curl: (22) The requested URL returned error: 404`,
		`#7 ERROR: process "/bin/sh -c ` + giant + `" did not complete successfully: exit code: 1`,
		`------`,
		`ERROR: failed to build: failed to solve: process "/bin/sh -c ` + giant + `" did not complete successfully: exit code: 1`,
	}, "\n")

	var buf bytes.Buffer
	sec := output.NewSection(&buf, "Image", 0, false)
	RenderBuildError(sec, stderr)
	sec.Close()
	out := buf.String()

	if strings.Contains(out, giant) {
		t.Fatal("the giant command was surfaced verbatim (not elided)")
	}
	for _, l := range strings.Split(out, "\n") {
		if n := len([]rune(l)); n > 300 {
			t.Errorf("surfaced line too long (%d runes): %.80q", n, l)
		}
	}
	if !strings.Contains(out, "404") {
		t.Error("the real failure (curl 404) was not surfaced")
	}
}
