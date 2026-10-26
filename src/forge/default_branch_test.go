package forge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSetDefaultBranch: each forge sets the default branch with its own verb on the repo
// endpoint.
func TestSetDefaultBranch(t *testing.T) {
	cases := []struct {
		name, method, path string
		forge              func(base string) DefaultBranchSetter
	}{
		{"github", "PATCH", "/repos/o/r", func(b string) DefaultBranchSetter {
			return &GitHubForge{BaseURL: b, Token: "t", Owner: "o", Repo: "r"}
		}},
		{"gitlab", "PUT", "/api/v4/projects/g%2Fp", func(b string) DefaultBranchSetter {
			return &GitLabForge{BaseURL: b, Token: "t", ProjectID: "g/p"}
		}},
		{"gitea", "PATCH", "/api/v1/repos/o/r", func(b string) DefaultBranchSetter {
			return &GiteaForge{BaseURL: b, Token: "t", Owner: "o", Repo: "r"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.EscapedPath() != tc.path {
					t.Errorf("request %s %s, want %s %s", r.Method, r.URL.EscapedPath(), tc.method, tc.path)
				}
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &got)
				_, _ = w.Write([]byte("{}"))
			}))
			defer srv.Close()
			if err := tc.forge(srv.URL).SetDefaultBranch(context.Background(), "autohotkey-original"); err != nil {
				t.Fatal(err)
			}
			if got["default_branch"] != "autohotkey-original" {
				t.Errorf("body %v", got)
			}
		})
	}
}
