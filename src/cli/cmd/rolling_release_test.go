package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/forge"
)

// TestRefreshRollingRelease verifies the rolling-alias refresh deletes the old
// release, recreates it, and re-uploads assets — and that a second run is
// idempotent (DeleteRelease tolerates a missing release on the first run; the
// sequence repeats cleanly).
func TestRefreshRollingRelease(t *testing.T) {
	var seq []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "DELETE" && strings.Contains(r.URL.Path, "/releases/"):
			seq = append(seq, "delete-release")
			w.WriteHeader(http.StatusOK)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/releases"):
			seq = append(seq, "create-release")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tag_name":"latest-dev"}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/uploads"):
			seq = append(seq, "upload")
			_, _ = w.Write([]byte(`{"url":"/uploads/x/dwiz.zip"}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/assets/links"):
			seq = append(seq, "link")
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	asset := filepath.Join(dir, "dwiz.zip")
	if err := os.WriteFile(asset, []byte("bin"), 0o644); err != nil {
		t.Fatal(err)
	}
	fc := &forge.GitLabForge{BaseURL: srv.URL, Token: "t", ProjectID: "g/p"}

	run := func() {
		if err := refreshRollingRelease(context.Background(), fc, "latest-dev", "deadbeef", "latest-dev", "notes", forge.ReleaseTypePrerelease, []string{asset}); err != nil {
			t.Fatalf("refresh: %v", err)
		}
	}
	const want = "delete-release,create-release,upload,link"

	run()
	if got := strings.Join(seq, ","); got != want {
		t.Fatalf("sequence = %q, want %q", got, want)
	}
	seq = nil
	run() // idempotent
	if got := strings.Join(seq, ","); got != want {
		t.Fatalf("second-run sequence = %q, want %q", got, want)
	}
}
