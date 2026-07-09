package pages

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A mock Cloudflare API server verifies the full Direct Upload protocol wiring —
// endpoints, auth (API token vs upload JWT), the check-missing body, the upload
// payload shape, and the deployment manifest — without a real Cloudflare account. The
// hash itself is separately pinned to wrangler's vectors (cloudflare_hash_test.go).
func TestCFPagesClient_DeployProtocol(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "index.html"), []byte("<html>hi</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}

	var (
		gotUploadToken bool
		gotCheckHashes []string
		gotUploadItems []cfUploadItem
		gotManifest    map[string]string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/upload-token"):
			gotUploadToken = true
			if r.Header.Get("Authorization") != "Bearer api-token" {
				t.Errorf("upload-token auth = %q, want the API token", r.Header.Get("Authorization"))
			}
			writeCFResult(w, map[string]any{"jwt": "the-jwt"})
		case r.URL.Path == "/pages/assets/check-missing":
			if r.Header.Get("Authorization") != "Bearer the-jwt" {
				t.Errorf("check-missing auth = %q, want the upload JWT", r.Header.Get("Authorization"))
			}
			var body struct {
				Hashes []string `json:"hashes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotCheckHashes = body.Hashes
			writeCFResult(w, body.Hashes) // report all as missing
		case r.URL.Path == "/pages/assets/upload":
			if r.Header.Get("Authorization") != "Bearer the-jwt" {
				t.Errorf("upload auth = %q, want the upload JWT", r.Header.Get("Authorization"))
			}
			var items []cfUploadItem
			_ = json.NewDecoder(r.Body).Decode(&items)
			gotUploadItems = append(gotUploadItems, items...)
			writeCFResult(w, true)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/deployments"):
			_ = r.ParseMultipartForm(1 << 20)
			_ = json.Unmarshal([]byte(r.FormValue("manifest")), &gotManifest)
			writeCFResult(w, map[string]any{"url": "https://abc.proj.pages.dev"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := newCFPagesClient("api-token", "acct-1", "proj")
	c.base = srv.URL

	url, err := c.deploy(context.Background(), ws)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	if !gotUploadToken {
		t.Error("upload-token was not requested")
	}
	if len(gotCheckHashes) != 2 {
		t.Errorf("check-missing hashes = %v, want 2 distinct", gotCheckHashes)
	}
	if len(gotUploadItems) != 2 {
		t.Errorf("uploaded items = %d, want 2", len(gotUploadItems))
	}
	for _, it := range gotUploadItems {
		if !it.Base64 || it.Value == "" || it.Metadata.ContentType == "" {
			t.Errorf("upload item malformed: %+v", it)
		}
	}
	if gotManifest["/index.html"] == "" || gotManifest["/assets/app.js"] == "" {
		t.Errorf("manifest = %v, want leading-slash keys → hashes", gotManifest)
	}
	if url != "https://abc.proj.pages.dev" {
		t.Errorf("deploy url = %q, want the deployment result url", url)
	}
}

func writeCFResult(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
}
