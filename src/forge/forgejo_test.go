package forge

import "testing"

// TestForgejoIdentity: Forgejo is a first-class identity over the recycled Gitea
// backend — it reports its own provider while inheriting the Gitea client's
// behavior (embedded fields/methods).
func TestForgejoIdentity(t *testing.T) {
	f := NewForgejo("https://codeberg.org/")
	if f.Provider() != Forgejo {
		t.Fatalf("Provider() = %q, want %q", f.Provider(), Forgejo)
	}
	// Embedded Gitea backend is wired (trailing slash trimmed by NewGitea).
	if f.BaseURL != "https://codeberg.org" {
		t.Fatalf("BaseURL = %q, want https://codeberg.org", f.BaseURL)
	}
	// Forgejo must satisfy the Forge interface via the embedded backend.
	var _ Forge = f
}

// TestDetectProviderForgejo pins that forgejo/codeberg are detected as Forgejo,
// not folded into Gitea, while the other forges are unaffected.
func TestDetectProviderForgejo(t *testing.T) {
	cases := map[string]Provider{
		"https://codeberg.org/org/repo.git":     Forgejo,
		"git@code.forgejo.org:org/repo.git":      Forgejo,
		"https://gitea.example.com/org/repo.git": Gitea,
		"https://github.com/org/repo.git":        GitHub,
		"https://gitlab.example.com/org/repo":    GitLab,
	}
	for url, want := range cases {
		if got := DetectProvider(url); got != want {
			t.Errorf("DetectProvider(%q) = %q, want %q", url, got, want)
		}
	}
}
