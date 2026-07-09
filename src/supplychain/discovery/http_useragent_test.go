package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// crates.io (and others) 403 a missing/default User-Agent. Every freshness request
// must identify StageFreight, or cargo deps silently go "unresolved".
func TestFetchJSON_SendsIdentifyingUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var out map[string]any
	if err := newHTTPClient(5).fetchJSON(context.Background(), srv.URL, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotUA, "StageFreight") {
		t.Errorf("User-Agent must identify StageFreight, got %q", gotUA)
	}
}
