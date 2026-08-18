package forge

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "asset.bin")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Bug 2: a name with a space must be query-escaped, not interpolated raw into the URL.
func TestAssetUploadURL_QueryEscapesName(t *testing.T) {
	g := &GitHubForge{BaseURL: "https://api.github.com", Owner: "o", Repo: "r"}
	got := g.assetUploadURL("123", "quay v0.0.5")
	if strings.Contains(got, "name=quay v0.0.5") || strings.Contains(got, "quay v0.0.5") {
		t.Errorf("URL contains a raw space: %q", got)
	}
	if !strings.Contains(got, "name=quay+v0.0.5") {
		t.Errorf("URL = %q, want the escaped name=quay+v0.0.5", got)
	}
}

// Bug 1: the request must carry a GetBody that REOPENS the file, so a redirecting transport
// can replay the body byte-for-byte.
func TestNewFileUploadRequest_GetBodyReopens(t *testing.T) {
	path := writeTempFile(t, "the-bytes")
	req, err := newFileUploadRequest(context.Background(), "https://uploads.example/x", path, "application/octet-stream", int64(len("the-bytes")))
	if err != nil {
		t.Fatal(err)
	}
	if req.GetBody == nil {
		t.Fatal("GetBody is nil — a redirecting/http2 upload cannot replay the body")
	}
	if req.ContentLength != int64(len("the-bytes")) {
		t.Errorf("ContentLength = %d, want %d", req.ContentLength, len("the-bytes"))
	}
	rc, err := req.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "the-bytes" {
		t.Errorf("GetBody reopened content = %q, want the-bytes", got)
	}
}

// End-to-end: uploads.github.com redirects, so UploadAsset must FOLLOW a 307 and replay the
// body. Without GetBody the client would not re-send the body (and the final endpoint never
// receives the bytes). This proves the transport contract and the name escaping together.
func TestUploadAsset_RedirectReplaysBodyViaGetBody(t *testing.T) {
	var rawQuery string
	var finalBody []byte
	hitFinal := false
	mux := http.NewServeMux()
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		finalBody, _ = io.ReadAll(r.Body)
		hitFinal = true
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	g := &GitHubForge{BaseURL: srv.URL + "/api/v3", Token: "x", Owner: "o", Repo: "r"}
	if err := g.UploadAsset(context.Background(), "123", Asset{Name: "quay v0.0.5", FilePath: writeTempFile(t, "payload")}); err != nil {
		t.Fatal(err)
	}
	if !hitFinal {
		t.Fatal("the 307 redirect was not followed — GetBody body-replay failed (Bug 1)")
	}
	if string(finalBody) != "payload" {
		t.Errorf("final endpoint body = %q, want payload (GetBody must reopen and replay the file)", finalBody)
	}
	if !strings.Contains(rawQuery, "name=quay+v0.0.5") {
		t.Errorf("upload query = %q, want an escaped name=quay+v0.0.5 (Bug 2)", rawQuery)
	}
}
