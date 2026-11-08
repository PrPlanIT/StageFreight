package cmd

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/cistate"
)

// A failing run ends narrate non-zero with the failures that made it fail; a run whose only
// failures are tolerated, or that passed, ends it zero.
func TestRunVerdict(t *testing.T) {
	render := func(st *cistate.State) (string, error) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		verr := runVerdict(w, st)
		w.Close()
		out, _ := io.ReadAll(r)
		return string(out), verr
	}

	failing := &cistate.State{Subsystems: []cistate.SubsystemState{
		{Name: "build", Attempted: true, Outcome: "success"},
		{Name: "publish", Attempted: true, Required: true, Outcome: "failed", Reason: "signing image (enforce): cosign sign: tls: access denied"},
		{Name: "docs", Attempted: true, AllowFailure: true, Outcome: "failed", Reason: "advisory"},
	}}
	out, err := render(failing)
	var exit *SilentExitError
	if !errors.As(err, &exit) || exit.Code != 1 {
		t.Fatalf("a failing run must end narrate non-zero, got %v", err)
	}
	if !strings.Contains(out, "Run failed") || !strings.Contains(out, "publish") || !strings.Contains(out, "access denied") {
		t.Fatalf("the failure that failed the run must be shown:\n%s", out)
	}
	if strings.Contains(out, "docs") || strings.Contains(out, "build") {
		t.Fatalf("only the failures that failed the run are shown:\n%s", out)
	}

	for name, st := range map[string]*cistate.State{
		"passing":   {Subsystems: []cistate.SubsystemState{{Name: "build", Attempted: true, Outcome: "success"}}},
		"tolerated": {Subsystems: []cistate.SubsystemState{{Name: "docs", Attempted: true, AllowFailure: true, Outcome: "failed"}}},
	} {
		if out, err := render(st); err != nil || out != "" {
			t.Errorf("%s run: err = %v, output %q; want a silent pass", name, err, out)
		}
	}
}
