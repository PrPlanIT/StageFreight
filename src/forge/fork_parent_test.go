package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestForkParent: each forge reads the parent's clone URL from its own repository record,
// and a repository that is not a fork has none.
func TestForkParent(t *testing.T) {
	cases := []struct {
		name, path, fork, notFork string
		forge                     func(base string) ForkParentReader
	}{
		{"github", "/repos/o/r",
			`{"fork":true,"parent":{"clone_url":"https://github.com/up/r.git"}}`, `{"fork":false}`,
			func(b string) ForkParentReader { return &GitHubForge{BaseURL: b, Token: "t", Owner: "o", Repo: "r"} }},
		{"gitlab", "/api/v4/projects/g%2Fp",
			`{"forked_from_project":{"http_url_to_repo":"https://github.com/up/r.git"}}`, `{}`,
			func(b string) ForkParentReader { return &GitLabForge{BaseURL: b, Token: "t", ProjectID: "g/p"} }},
		{"gitea", "/api/v1/repos/o/r",
			`{"fork":true,"parent":{"clone_url":"https://github.com/up/r.git"}}`, `{"fork":false,"parent":null}`,
			func(b string) ForkParentReader { return &GiteaForge{BaseURL: b, Token: "t", Owner: "o", Repo: "r"} }},
		{"azuredevops", "/proj/_apis/git/repositories/r",
			`{"isFork":true,"parentRepository":{"remoteUrl":"https://github.com/up/r.git"}}`, `{"isFork":false}`,
			func(b string) ForkParentReader {
				return &AzureDevOpsForge{BaseURL: b, Token: "t", Project: "proj", Repo: "r"}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.fork
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.EscapedPath() != tc.path {
					t.Errorf("request %s %s, want GET %s", r.Method, r.URL.EscapedPath(), tc.path)
				}
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			got, err := tc.forge(srv.URL).ForkParent(context.Background())
			if err != nil || got != "https://github.com/up/r.git" {
				t.Errorf("fork: %q, %v", got, err)
			}
			body = tc.notFork
			if got, err := tc.forge(srv.URL).ForkParent(context.Background()); err != nil || got != "" {
				t.Errorf("not a fork: %q, %v", got, err)
			}
		})
	}
}
