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

// A failed step's own output says why it failed; BuildKit prints it before the
// Dockerfile excerpt that ends stderr, so it is surfaced by step, not by stderr tail.
func TestStepTail(t *testing.T) {
	stderr := strings.Join([]string{
		"#13 [build 4/9] RUN pnpm install",
		"#13 12.1 Packages: +1450",
		"#13 DONE 40.2s",
		"#14 [build 5/9] RUN pnpm nx run @zitadel/api:generate --parallel=1",
		"#14 3.0 > nx run @zitadel/api:generate",
		"#14 217.5 Failed tasks:",
		"#14 217.5 - @zitadel/proto:generate",
		"#15 0.4 unrelated parallel step",
		"#14 217.6 buf: plugin protoc-gen-go: exit status 2",
		`#14 ERROR: process "/bin/sh -c pnpm nx run @zitadel/api:generate --parallel=1" did not complete successfully: exit code: 130`,
		"------",
		"  42 | >>> RUN pnpm nx run @zitadel/api:generate --parallel=1",
		"  43 |     RUN pnpm nx run @zitadel/api:build-console",
		"------",
	}, "\n")
	id, tail := stepTail(strings.Split(stderr, "\n"), 20)
	if id != "14" {
		t.Fatalf("step = %q, want 14", id)
	}
	want := []string{"> nx run @zitadel/api:generate", "Failed tasks:", "- @zitadel/proto:generate", "buf: plugin protoc-gen-go: exit status 2"}
	if strings.Join(tail, "|") != strings.Join(want, "|") {
		t.Errorf("tail = %q, want %q", tail, want)
	}
	if _, tail := stepTail(strings.Split(stderr, "\n"), 2); len(tail) != 2 || tail[1] != "buf: plugin protoc-gen-go: exit status 2" {
		t.Errorf("bounded tail = %q, want the last 2 lines", tail)
	}
	if id, tail := stepTail([]string{"#3 0.1 fine", "#3 DONE 0.2s"}, 20); id != "" || tail != nil {
		t.Errorf("no failed step: got %q %q", id, tail)
	}
}
