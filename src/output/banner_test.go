package output

import (
	"bytes"
	"strings"
	"testing"
)

// IdentityLine is the per-phase provenance stamp. It must render version, SHA,
// and branch on one line, omit empty fields, and never emit the logo art.
func TestIdentityLine_NoColorAllFields(t *testing.T) {
	var buf bytes.Buffer
	IdentityLine(&buf, BannerInfo{Version: "1.2.3", SHA: "abc1234", Branch: "main"}, false)

	out := buf.String()
	if !strings.Contains(out, "StageFreight 1.2.3") {
		t.Errorf("missing name+version; got %q", out)
	}
	if !strings.Contains(out, "abc1234") || !strings.Contains(out, "main") {
		t.Errorf("missing sha/branch; got %q", out)
	}
	if !strings.Contains(out, "·") {
		t.Errorf("expected separators between fields; got %q", out)
	}
	// One logical identity line (plus surrounding blank lines) — never multi-line art.
	nonBlank := 0
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(ln) != "" {
			nonBlank++
		}
	}
	if nonBlank != 1 {
		t.Errorf("IdentityLine = %d non-blank lines, want 1 (no art); got %q", nonBlank, out)
	}
}

// Empty SHA/branch must be dropped (no dangling separators), e.g. a local run
// with no commit metadata.
func TestIdentityLine_OmitsEmptyFields(t *testing.T) {
	var buf bytes.Buffer
	IdentityLine(&buf, BannerInfo{Version: "1.2.3"}, false)

	line := strings.TrimSpace(buf.String())
	if line != "StageFreight 1.2.3" {
		t.Errorf("IdentityLine with version only = %q, want %q", line, "StageFreight 1.2.3")
	}
}

// The Date field belongs to the full banner, not the slim line.
func TestIdentityLine_OmitsDate(t *testing.T) {
	var buf bytes.Buffer
	IdentityLine(&buf, BannerInfo{Version: "1.2.3", SHA: "abc1234", Date: "2026-06-07"}, false)

	if strings.Contains(buf.String(), "2026-06-07") {
		t.Errorf("IdentityLine must not render Date; got %q", buf.String())
	}
}

func TestIdentityLine_ColorWrapsANSI(t *testing.T) {
	var buf bytes.Buffer
	IdentityLine(&buf, BannerInfo{Version: "1.2.3", SHA: "abc1234"}, true)

	if !strings.Contains(buf.String(), "\033[") {
		t.Errorf("color output should contain ANSI escapes; got %q", buf.String())
	}
}
