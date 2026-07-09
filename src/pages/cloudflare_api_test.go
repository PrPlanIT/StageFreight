package pages

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A mock Cloudflare API server verifies the full Direct Upload protocol wiring —
// idempotent project create, endpoints, auth (API token vs upload JWT), the
// check-missing body, the upload payload shape, the deployment manifest, and the
// custom-domain attach — without a real Cloudflare account. The hash itself is
// separately pinned to wrangler's vectors (cloudflare_hash_test.go).
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
		gotProjectGet  bool
		gotCreate      bool
		gotUploadToken bool
		gotCheckHashes []string
		gotUploadItems []cfUploadItem
		gotManifest    map[string]string
		gotDomain      string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/upload-token"):
			gotUploadToken = true
			if r.Header.Get("Authorization") != "Bearer api-token" {
				t.Errorf("upload-token auth = %q, want the API token", r.Header.Get("Authorization"))
			}
			writeCFResult(w, map[string]any{"jwt": "the-jwt"})
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/pages/projects/proj"):
			// Existence check → report not-found so the client creates it.
			gotProjectGet = true
			writeCFError(w, http.StatusNotFound, "project not found")
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/pages/projects"):
			gotCreate = true
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Name != "proj" {
				t.Errorf("create project name = %q, want proj", body.Name)
			}
			writeCFResult(w, map[string]any{"name": body.Name})
		case p == "/pages/assets/check-missing":
			if r.Header.Get("Authorization") != "Bearer the-jwt" {
				t.Errorf("check-missing auth = %q, want the upload JWT", r.Header.Get("Authorization"))
			}
			var body struct {
				Hashes []string `json:"hashes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotCheckHashes = body.Hashes
			writeCFResult(w, body.Hashes)
		case p == "/pages/assets/upload":
			var items []cfUploadItem
			_ = json.NewDecoder(r.Body).Decode(&items)
			gotUploadItems = append(gotUploadItems, items...)
			writeCFResult(w, true)
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/deployments"):
			_ = r.ParseMultipartForm(1 << 20)
			_ = json.Unmarshal([]byte(r.FormValue("manifest")), &gotManifest)
			writeCFResult(w, map[string]any{"url": "https://abc.proj.pages.dev"})
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/domains"):
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotDomain = body.Name
			writeCFResult(w, map[string]any{"name": body.Name})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, p)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := newCFPagesClient("api-token", "acct-1", "proj", "docs.example.com")
	c.base = srv.URL
	// Inject NS so the attach's DNS classification is deterministic and offline.
	c.lookupNS = func(string) ([]*net.NS, error) {
		return []*net.NS{{Host: "adam.ns.cloudflare.com."}, {Host: "kara.ns.cloudflare.com."}}, nil
	}

	res, err := c.deploy(context.Background(), ws)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	url := res.URL

	if !gotProjectGet || !gotCreate {
		t.Errorf("project not created idempotently: get=%v create=%v", gotProjectGet, gotCreate)
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
	if gotDomain != "docs.example.com" {
		t.Errorf("custom domain attach = %q, want docs.example.com", gotDomain)
	}
	if url != "https://abc.proj.pages.dev" {
		t.Errorf("deploy url = %q, want the deployment result url", url)
	}
	// Domain outcome is reported as data alongside the deploy, not folded into it.
	if res.Domain == nil {
		t.Fatal("res.Domain = nil, want a domain outcome for the requested custom domain")
	}
	if !res.Domain.Attached {
		t.Errorf("res.Domain.Attached = false, want true (attach succeeded)")
	}
	if res.Domain.DNSProvider != DNSCloudflare {
		t.Errorf("res.Domain.DNSProvider = %q, want cloudflare (all NS end in .cloudflare.com)", res.Domain.DNSProvider)
	}
	if res.Domain.Err != "" {
		t.Errorf("res.Domain.Err = %q, want empty on success", res.Domain.Err)
	}
}

func writeCFResult(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
}

func writeCFError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"errors":  []map[string]any{{"code": 8000007, "message": msg}},
	})
}
