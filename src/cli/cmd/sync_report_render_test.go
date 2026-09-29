package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// The Sync section must render like every other subsystem, and every field must be
// a count or a capped list — nothing here may scale with the size of the repository.
func TestRenderSyncSection(t *testing.T) {
	rep := syncMirrorReport{id: "github-mirror"}
	rep.add("warning", "refs", "DEGRADED — %s: %s", "unknown", "object not found")
	rep.note("%-10s %s", "plan", planDetail(12, 2, 1))
	rep.add("success", "releases", "%d created · %d updated · %d adopted · %d in sync · %d pruned", 2, 1, 1, 3, 0)
	rep.note("%-10s %s", "adopted", capList([]string{"v0.39.1"}))
	rep.add("success", "retention", "dev-release  %d kept · %d pruned", 6, 2)

	var buf bytes.Buffer
	renderSyncSection(&buf, []syncMirrorReport{rep}, 4100*time.Millisecond, true)
	got := buf.String()
	t.Logf("\n%s", got)

	for _, want := range []string{"Sync", "github-mirror", "refs", "12 refspecs · 2 pruned · 1 foreign kept", "adopted", "replication degraded"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Bounding: a huge adoption set must not produce a huge line.
	many := make([]string, 200)
	for i := range many {
		many[i] = "v9.9.9-verbose-tag-name"
	}
	if l := len(capList(many)); l > 200 {
		t.Errorf("capList unbounded: %d chars", l)
	}
	for _, line := range strings.Split(got, "\n") {
		if len(line) > 200 {
			t.Errorf("line exceeds 200 chars (%d): %s", len(line), line)
		}
	}
}

// The healthy path is what most runs render — it must stay quiet and scannable.
func TestRenderSyncSection_Healthy(t *testing.T) {
	gh := syncMirrorReport{id: "github-mirror"}
	gh.add("success", "refs", "pushed in %s", 1200*time.Millisecond)
	gh.note("%-10s %s", "plan", planDetail(9, 0, 1))
	gh.add("success", "releases", "%d created · %d updated · %d adopted · %d in sync · %d pruned", 0, 0, 0, 7, 0)

	gt := syncMirrorReport{id: "gitea-mirror"}
	gt.add("success", "refs", "pushed in %s", 800*time.Millisecond)
	gt.note("%-10s %s", "plan", planDetail(9, 1, 0))
	gt.add("success", "releases", "%d created · %d updated · %d adopted · %d in sync · %d pruned", 1, 0, 0, 6, 1)
	gt.add("success", "retention", "dev-release  %d kept · %d pruned", 6, 1)

	var buf bytes.Buffer
	renderSyncSection(&buf, []syncMirrorReport{gh, gt}, 3400*time.Millisecond, false)
	t.Logf("\n%s", buf.String())
	if strings.Contains(buf.String(), "degraded") {
		t.Error("healthy run must not mention degradation")
	}
}
