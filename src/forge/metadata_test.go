package forge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubUpdateRepoMetadata(t *testing.T) {
	var gotPatch, gotTopics map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == "PATCH" && r.URL.Path == "/repos/o/r":
			_ = json.Unmarshal(body, &gotPatch)
		case r.Method == "PUT" && r.URL.Path == "/repos/o/r/topics":
			_ = json.Unmarshal(body, &gotTopics)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	g := &GitHubForge{BaseURL: srv.URL, Token: "t", Owner: "o", Repo: "r"}
	out, err := g.UpdateRepoMetadata(context.Background(), RepoMetadata{
		Description: "short tagline",
		Website:     "https://example.com",
		Topics:      []string{"ci-cd", "gitops"},
		LogoPath:    "logo.png",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if gotPatch["description"] != "short tagline" || gotPatch["homepage"] != "https://example.com" {
		t.Fatalf("PATCH body wrong: %v", gotPatch)
	}
	names, _ := gotTopics["names"].([]any)
	if len(names) != 2 || names[0] != "ci-cd" {
		t.Fatalf("topics body wrong: %v", gotTopics)
	}
	if len(out.Set) != 3 { // description + website + topics
		t.Fatalf("expected 3 fields set, got %v", out.Set)
	}
	if len(out.Skipped) != 1 { // logo — org-scoped only on GitHub
		t.Fatalf("expected logo skipped, got %v", out.Skipped)
	}
}

func TestNormalizeTopic(t *testing.T) {
	cases := map[string]string{
		"Machine Learning": "machine-learning",
		"CI-CD":            "ci-cd",
		"gitops":           "gitops",
		"C++":              "c",
		"  Hello  World  ": "hello-world",
	}
	for in, want := range cases {
		if got, _ := NormalizeTopic(in); got != want {
			t.Errorf("NormalizeTopic(%q) = %q, want %q", in, got, want)
		}
	}
	if _, changed := NormalizeTopic("Machine Learning"); !changed {
		t.Error("expected changed=true for 'Machine Learning'")
	}
	if _, changed := NormalizeTopic("gitops"); changed {
		t.Error("expected changed=false for an already-canonical topic")
	}
}
