//go:build forgelive

// Live end-to-end verification of the Gitea/Forgejo re-publish path against real instances
// (see dungeon-scratchpad/forge-test/docker-compose.yml). Build-tagged so normal `go test`
// skips it. Run:
//
//	go test -tags forgelive ./src/forge/ -run Live -v
//	  -e GITEA_URL=http://127.0.0.1:3000  -e GITEA_TOKEN=…
//	  -e FORGEJO_URL=http://127.0.0.1:3001 -e FORGEJO_TOKEN=…   (docker run --network host)
package forge

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func liveForge(t *testing.T, urlEnv, tokEnv string) *GiteaForge {
	base, tok := os.Getenv(urlEnv), os.Getenv(tokEnv)
	if base == "" || tok == "" {
		t.Skipf("%s/%s not set", urlEnv, tokEnv)
	}
	return &GiteaForge{BaseURL: base, Token: tok, Owner: "tester", Repo: "rel-test"}
}

func liveRelease(t *testing.T, g *GiteaForge, tag string) (publishedAt, body string, draft bool) {
	var r struct {
		PublishedAt string `json:"published_at"`
		Body        string `json:"body"`
		Draft       bool   `json:"draft"`
	}
	if err := g.doJSON(context.Background(), "GET", g.apiURL("/releases/tags/"+tag), nil, &r); err != nil {
		t.Fatalf("read release %s: %v", tag, err)
	}
	return r.PublishedAt, r.Body, r.Draft
}

// A re-cut through CreateRelease (which hits 409 and routes to the in-place update) must:
// refresh the publish date, update the content, and leave the release VISIBLE (not drafted).
func runLiveRepublish(t *testing.T, g *GiteaForge) {
	ctx := context.Background()
	tag := "vlive-" + time.Now().Format("150405.000")

	if _, err := g.CreateRelease(ctx, ReleaseOptions{
		TagName: tag, Ref: "main", Name: tag, Description: "first", Type: ReleaseTypeLatest,
	}); err != nil {
		t.Fatalf("initial create: %v", err)
	}
	before, _, _ := liveRelease(t, g, tag)

	time.Sleep(2 * time.Second)

	// Same tag again → 409 → in-place update + draft-toggle.
	if _, err := g.CreateRelease(ctx, ReleaseOptions{
		TagName: tag, Ref: "main", Name: tag, Description: "second", Type: ReleaseTypeLatest,
	}); err != nil {
		t.Fatalf("re-cut (409 must route to update, not fail): %v", err)
	}
	after, body, draft := liveRelease(t, g, tag)

	if draft {
		t.Errorf("release left as DRAFT (invisible) after re-cut")
	}
	if after == before {
		t.Errorf("published_at NOT refreshed on re-cut: still %s", after)
	}
	if body != "second" {
		t.Errorf("content not updated: body = %q, want \"second\"", body)
	}
	t.Logf("re-cut OK: published_at %s -> %s, body=%q, visible", before, after, body)

	_ = g.DeleteRelease(ctx, tag) // best-effort cleanup
}

func TestLive_Gitea_Republish(t *testing.T) {
	runLiveRepublish(t, liveForge(t, "GITEA_URL", "GITEA_TOKEN"))
}
func TestLive_Forgejo_Republish(t *testing.T) {
	runLiveRepublish(t, liveForge(t, "FORGEJO_URL", "FORGEJO_TOKEN"))
}

// runLiveUpdateRelease exercises the exact method the CI mirror uses on a re-cut —
// forge.UpdateRelease — and verifies it re-stamps published_at (the thing that was silently
// broken: the mirror path bypassed the draft-toggle) while updating content and staying
// visible.
func runLiveUpdateRelease(t *testing.T, g *GiteaForge) {
	ctx := context.Background()
	tag := "vupd-" + time.Now().Format("150405.000")

	if _, err := g.CreateRelease(ctx, ReleaseOptions{
		TagName: tag, Ref: "main", Name: tag, Description: "first", Type: ReleaseTypeLatest,
	}); err != nil {
		t.Fatalf("initial create: %v", err)
	}
	before, _, _ := liveRelease(t, g, tag)

	var r struct {
		ID int `json:"id"`
	}
	if err := g.doJSON(ctx, "GET", g.apiURL("/releases/tags/"+tag), nil, &r); err != nil {
		t.Fatalf("resolve id: %v", err)
	}

	time.Sleep(2 * time.Second)

	// The mirror-convergence path, directly.
	if err := g.UpdateRelease(ctx, fmt.Sprintf("%d", r.ID),
		ReleaseMeta{Name: tag, Description: "updated", Type: ReleaseTypeLatest}); err != nil {
		t.Fatalf("UpdateRelease: %v", err)
	}
	after, body, draft := liveRelease(t, g, tag)

	if draft {
		t.Errorf("release left as DRAFT after UpdateRelease")
	}
	if after == before {
		t.Errorf("UpdateRelease did NOT re-stamp published_at: still %s", after)
	}
	if body != "updated" {
		t.Errorf("content not updated: body = %q, want \"updated\"", body)
	}
	t.Logf("UpdateRelease OK: published_at %s -> %s, body=%q, visible", before, after, body)

	_ = g.DeleteRelease(ctx, tag)
}

func TestLive_Gitea_UpdateRelease(t *testing.T) {
	runLiveUpdateRelease(t, liveForge(t, "GITEA_URL", "GITEA_TOKEN"))
}
func TestLive_Forgejo_UpdateRelease(t *testing.T) {
	runLiveUpdateRelease(t, liveForge(t, "FORGEJO_URL", "FORGEJO_TOKEN"))
}
