package gitstate

import (
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// clearGitCredEnv blanks every credential env the transport decision reads, so a value set
// in the real environment cannot leak into an assertion.
func clearGitCredEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"SSH_PRIVATE_KEY", "STAGEFREIGHT_GIT_PASSWORD", "STAGEFREIGHT_GIT_USERNAME",
		"GITLAB_TOKEN", "GITHUB_TOKEN", "GITEA_TOKEN", "FORGEJO_TOKEN", "CI_JOB_TOKEN",
		"CI_SERVER_HOST", "GL_TOKEN", "GH_TOKEN", "EXAMPLE_TOKEN",
	} {
		t.Setenv(k, "")
	}
}

func cfgWithForges(forges ...config.ForgeConfig) *config.Config {
	return &config.Config{Forges: forges}
}

// Each remote's credential is host-bound: the owning forge's token for its host, and
// nothing for a host no forge claims, whatever tokens the environment holds.
func TestEmbeddedTransport_HostBoundCredentialPerRemote(t *testing.T) {
	clearGitCredEnv(t)
	t.Setenv("EXAMPLE_TOKEN", "glpat-test")
	t.Setenv("GITHUB_TOKEN", "ghp-should-not-be-sent-anywhere")
	repo, err := git.PlainInit(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	for name, url := range map[string]string{"origin": "https://example.com/o/r.git", "other": "https://unclaimed.example.com/o/r.git"} {
		if _, err := repo.CreateRemote(&gitconfig.RemoteConfig{Name: name, URLs: []string{url}}); err != nil {
			t.Fatal(err)
		}
	}
	tr := newEmbeddedTransport(repo, cfgWithForges(config.ForgeConfig{Provider: "gitlab", URL: "https://example.com", Credentials: "EXAMPLE"}))
	if auth, err := tr.auth("origin"); err != nil || auth == nil {
		t.Fatalf("owning forge: auth=%v err=%v, want its token", auth, err)
	}
	if auth, err := tr.auth("other"); err != nil || auth != nil {
		t.Fatalf("unclaimed host: auth=%v err=%v, want anonymous", auth, err)
	}
}

// A session over an SSH remote opens without any key; a key is needed only once the
// remote is actually reached.
func TestEmbeddedTransport_CredentialResolvedOnUse(t *testing.T) {
	clearGitCredEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SSH_AUTH_SOCK", "")
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{"git@example.com:o/r.git"}}); err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	if _, err := wt.Commit("c", &git.CommitOptions{AllowEmptyCommits: true, Author: &object.Signature{Name: "t", Email: "t@t"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSyncSession(dir, nil); err != nil {
		t.Fatalf("opening a session must not need a key: %v", err)
	}
	if _, err := newEmbeddedTransport(repo, nil).auth("origin"); err == nil {
		t.Fatal("reaching an SSH remote with no key must fail, naming what is missing")
	}
}

// SSH remotes are recognized the way go-git parses them, scp-style aliases included.
func TestIsSSHURL(t *testing.T) {
	for url, want := range map[string]bool{
		"ssh://git@host:2424/o/r.git": true,
		"git@host:o/r.git":            true,
		"kai@host:o/r.git":            true,
		"gitlab-alias:o/r.git":        true,
		"https://host/o/r.git":        false,
		"/srv/repos/r.git":            false,
		"file:///srv/repos/r.git":     false,
	} {
		if got := isSSHURL(url); got != want {
			t.Errorf("isSSHURL(%q) = %v, want %v", url, got, want)
		}
	}
}

type fakeSSHConfig map[string]map[string][]string

func (f fakeSSHConfig) Get(alias, key string) string {
	if v := f[alias][key]; len(v) > 0 {
		return v[0]
	}
	return ""
}
func (f fakeSSHConfig) GetAll(alias, key string) []string { return f[alias][key] }

// ~/.ssh/config supplies the user and keys for a host the URL doesn't name them for.
func TestSSHConfigUserAndIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := sshConfig
	t.Cleanup(func() { sshConfig = old })
	sshConfig = fakeSSHConfig{"gitlab-alias": {"User": {"deploy"}, "IdentityFile": {"~/.ssh/work_key"}}}

	if u := sshUser("gitlab-alias:o/r.git"); u != "deploy" {
		t.Errorf("config user = %q, want deploy", u)
	}
	if u := sshUser("kai@gitlab-alias:o/r.git"); u != "kai" {
		t.Errorf("the URL's own user must win, got %q", u)
	}
	if u := sshUser("git@other:o/r.git"); u != "git" {
		t.Errorf("default user = %q, want git", u)
	}
	files := sshIdentityFiles("gitlab-alias:o/r.git")
	if len(files) == 0 || files[0] != filepath.Join(home, ".ssh", "work_key") {
		t.Fatalf("identity files = %v, want the configured key first", files)
	}
}
