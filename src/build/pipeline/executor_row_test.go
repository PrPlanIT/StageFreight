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
	executorRow(sec, "workspace", "writable "+ok, "docker", "available "+ok)
	executorRow(sec, "memory", "28.0 GB of 32.0 GB "+ok, "buildx", "available")
	executorRow(sec, "disk", "12.6 GB free "+ok, "dind", "detected")
	want := -1
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		for _, key := range []string{"docker", "buildx", "dind"} {
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
