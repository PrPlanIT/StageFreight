package cmd

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/cistate"
)

func TestRenderWatchDrawsOneUpstreamSection(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	var rows upstreamRows
	rows.add("zitadel", "newest %s · %s %s", "v4.20.1", "git", "https://github.com/zitadel/zitadel")
	renderWatch(rows, cistate.SubsystemState{Outcome: "success", Reason: "upstream watched"})
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	t.Log("\n" + string(out))

	got := string(out)
	if strings.Count(got, "Upstream") != 1 {
		t.Fatalf("want one Upstream section:\n%s", got)
	}
	for _, want := range []string{"zitadel", "newest v4.20.1", "result", "upstream watched"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "upstream: ") {
		t.Fatalf("loose upstream line printed:\n%s", got)
	}
}

func TestUpstreamRowsNilDropsRows(t *testing.T) {
	var rows *upstreamRows
	rows.add("x", "y") // must not panic
}
