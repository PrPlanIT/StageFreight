package gitstate

import "testing"

// TestResolveHTTPAuth pins the credential resolution order for HTTPS push auth.
// This is the path the deps auto-commit push takes in CI; before it was a stub
// returning nil (no creds → "HTTP Basic: Access denied"). The order matters: a
// write-scoped token (explicit override / GITLAB_TOKEN) must win over the
// read-only CI_JOB_TOKEN, or pushes silently fall back to a token that cannot write.
func TestResolveHTTPAuth(t *testing.T) {
	// All four env vars are cleared per-case via t.Setenv("", ...) so a value set
	// in the real environment can't leak into the assertion.
	const (
		sfUser = "STAGEFREIGHT_GIT_USERNAME"
		sfPass = "STAGEFREIGHT_GIT_PASSWORD"
		glTok  = "GITLAB_TOKEN"
		ghTok  = "GITHUB_TOKEN"
		jobTok = "CI_JOB_TOKEN"
	)
	cases := []struct {
		name             string
		env              map[string]string
		wantUser, wantPw string
		wantNil          bool
	}{
		{"none set → nil", map[string]string{}, "", "", true},
		{"explicit user+pass", map[string]string{sfUser: "ci-bot", sfPass: "s3cret"}, "ci-bot", "s3cret", false},
		{"explicit pass only defaults user oauth2", map[string]string{sfPass: "s3cret"}, "oauth2", "s3cret", false},
		{"gitlab token", map[string]string{glTok: "glpat-xxx"}, "oauth2", "glpat-xxx", false},
		{"github token", map[string]string{ghTok: "ghp-xxx"}, "x-access-token", "ghp-xxx", false},
		{"ci job token last resort", map[string]string{jobTok: "job-xxx"}, "gitlab-ci-token", "job-xxx", false},
		{"explicit wins over gitlab+job", map[string]string{sfPass: "win", glTok: "glpat", jobTok: "job"}, "oauth2", "win", false},
		{"gitlab wins over job", map[string]string{glTok: "glpat", jobTok: "job"}, "oauth2", "glpat", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, k := range []string{sfUser, sfPass, glTok, ghTok, jobTok} {
				t.Setenv(k, c.env[k])
			}
			got, err := ResolveHTTPAuth("https://gitlab.example.com/org/repo.git")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.wantNil {
				if got != nil {
					t.Fatalf("expected nil auth, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected auth, got nil")
			}
			if got.Username != c.wantUser || got.Password != c.wantPw {
				t.Fatalf("got %s:%s, want %s:%s", got.Username, got.Password, c.wantUser, c.wantPw)
			}
		})
	}
}
