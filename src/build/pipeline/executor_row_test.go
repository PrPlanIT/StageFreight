package pipeline

import (
	"bytes"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/output/layout"
)

func TestExecutorRowAlignsSecondColumnPastColoredIcons(t *testing.T) {
	var buf bytes.Buffer
	sec := output.NewSection(&buf, "Executor", 0, false)
	ok := output.StatusIcon("success", true)
	executorRow(sec, "workspace", "writable "+ok, "free", "12.6 GB "+ok)
	executorRow(sec, "memory", "28.0 GB of 32.0 GB "+ok, "cpu", "1.41 avg")
	executorRow(sec, "dind", "detected", "buildx", "available")
	want := -1
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		for _, key := range []string{"free", "cpu", "buildx"} {
			if i := strings.Index(line, " "+key+" "); i >= 0 {
				col := layout.VisualWidth(line[:i])
				if want >= 0 && col != want {
					t.Errorf("%q column at %d, want %d:\n%s", key, col, want, buf.String())
				}
				want = col
			}
		}
	}
	if want < 0 {
		t.Fatalf("no second column found:\n%s", buf.String())
	}
}
