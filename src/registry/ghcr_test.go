package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ghcrFake serves a package's versions and records DELETEs. Version 1 is a dev build
// whose digest was promoted to a release (it carries dev-aaaa, v1.0.0 and latest);
// version 2 carries only dev-bbbb.
func ghcrFake(t *testing.T, deleted *[]string) *GHCR {
	t.Helper()
	versions := []map[string]any{
		{"id": 1, "name": "sha256:1111", "created_at": "2026-08-26T00:00:00Z",
			"metadata": map[string]any{"container": map[string]any{"tags": []string{"dev-aaaa", "v1.0.0", "latest"}}}},
		{"id": 2, "name": "sha256:2222", "created_at": "2026-08-27T00:00:00Z",
			"metadata": map[string]any{"container": map[string]any{"tags": []string{"dev-bbbb"}}}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/versions"):
			if r.URL.Query().Get("page") == "1" {
				_ = json.NewEncoder(w).Encode(versions)
				return
			}
			_, _ = w.Write([]byte("[]"))
		case r.Method == http.MethodDelete:
			*deleted = append(*deleted, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return &GHCR{client: httpClient{base: srv.URL}}
}

func TestGHCRDeleteTagSkipsSharedVersion(t *testing.T) {
	var deleted []string
	g := ghcrFake(t, &deleted)

	err := g.DeleteTag(context.Background(), "homelabhd/ark-se-server", "dev-aaaa")
	var shared *SharedVersionError
	if !errors.As(err, &shared) {
		t.Fatalf("DeleteTag(dev-aaaa) = %v, want a SharedVersionError", err)
	}
	if !shared.IsSkipped() {
		t.Fatal("SharedVersionError must report IsSkipped, so retention records a skip, not a failure")
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted %v: a version carrying v1.0.0 and latest must never be deleted", deleted)
	}
}

func TestGHCRDeleteTagDeletesSoleTagVersion(t *testing.T) {
	var deleted []string
	g := ghcrFake(t, &deleted)

	if err := g.DeleteTag(context.Background(), "homelabhd/ark-se-server", "dev-bbbb"); err != nil {
		t.Fatalf("DeleteTag(dev-bbbb) = %v, want nil", err)
	}
	if len(deleted) != 1 || !strings.HasSuffix(deleted[0], "/versions/2") {
		t.Fatalf("deleted %v, want exactly version 2", deleted)
	}
}
