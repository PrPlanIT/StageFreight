package gitver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchHarborInfo(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		switch {
		case strings.HasSuffix(r.URL.Path, "/repositories/homelabhelpdesk.com"):
			json.NewEncoder(w).Encode(map[string]any{"pull_count": 1234})
		case strings.HasSuffix(r.URL.Path, "/artifacts"):
			if r.URL.Query().Get("page") != "1" {
				json.NewEncoder(w).Encode([]any{}) // page 2 empty → stop
				return
			}
			json.NewEncoder(w).Encode([]map[string]any{
				{"push_time": "2026-08-10T00:00:00Z", "tags": []map[string]string{{"name": "v1.1.0"}}},
				{"push_time": "2026-08-11T00:00:00Z", "tags": []map[string]string{{"name": "v1.2.3"}}},
				{"push_time": "2026-08-12T00:00:00Z", "tags": []map[string]string{{"name": "v1.3.0"}, {"name": "latest"}}},
				{"push_time": "2026-08-13T00:00:00Z", "tags": []map[string]string{{"name": "latest-dev"}, {"name": "dev-abc1234"}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	info, err := FetchHarborInfo(srv.URL, "prplanit", "homelabhelpdesk.com", "robot$sf", "s3cr3t")
	if err != nil {
		t.Fatalf("FetchHarborInfo: %v", err)
	}
	if info.Pulls != 1234 {
		t.Errorf("Pulls = %d, want 1234", info.Pulls)
	}
	if info.LatestStable != "1.3.0" {
		t.Errorf("LatestStable = %q, want 1.3.0", info.LatestStable)
	}
	if info.LatestDev != "dev-abc1234" {
		t.Errorf("LatestDev = %q, want dev-abc1234", info.LatestDev)
	}
	if sawAuth == "" || !strings.HasPrefix(sawAuth, "Basic ") {
		t.Errorf("expected Basic auth header, got %q", sawAuth)
	}
}

func TestResolveHarborTemplates(t *testing.T) {
	info := &HarborInfo{Pulls: 1234, LatestStable: "1.3.0", LatestDev: "dev-abc1234"}

	got := ResolveHarborTemplates("v{harbor.version} · {harbor.dev} · {harbor.pulls} · {harbor.pulls:raw}", info)
	want := "v1.3.0 · dev-abc1234 · 1.2k · 1234"
	if got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}

	// Passthrough: nil info or no token left unchanged.
	if s := ResolveHarborTemplates("{harbor.version}", nil); s != "{harbor.version}" {
		t.Errorf("nil info should pass through, got %q", s)
	}
	if s := ResolveHarborTemplates("no tokens here", info); s != "no tokens here" {
		t.Errorf("no-token string should pass through, got %q", s)
	}
}
