package config

import "testing"

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://gitlab.example.com/o/r.git":     "gitlab.example.com",
		"https://gitlab.example.com:443/o/r":     "gitlab.example.com",
		"https://GitLab.Example.COM/o/r":         "gitlab.example.com", // case-folded
		"gitlab.example.com":                     "gitlab.example.com", // bare host
		"http://gitea.local:3000/o/r":            "gitea.local",
		"https://x-access-token:tok@github.com/": "github.com", // userinfo stripped
		"":                                       "",
	}
	for in, want := range cases {
		if got := HostOf(in); got != want {
			t.Errorf("HostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// ForgeForURL matches EXACTLY on host — never a prefix/substring — so a look-alike host
// can never borrow another forge's credential.
func TestForgeForURL_ExactHostMatch(t *testing.T) {
	forges := []ForgeConfig{
		{ID: "gl", Provider: "gitlab", URL: "https://gitlab.example.com"},
		{ID: "gh", Provider: "github", URL: "https://github.com"},
	}
	if f := ForgeForURL("https://gitlab.example.com/o/r.git", forges, nil); f == nil || f.ID != "gl" {
		t.Errorf("gitlab URL should match the gitlab forge, got %v", f)
	}
	if f := ForgeForURL("https://github.com/o/r.git", forges, nil); f == nil || f.ID != "gh" {
		t.Errorf("github URL should match the github forge, got %v", f)
	}
	// A host that merely CONTAINS a configured host as a substring must NOT match.
	if f := ForgeForURL("https://gitlab.example.com.evil.test/o/r.git", forges, nil); f != nil {
		t.Errorf("a look-alike host must NOT match (host-confusion guard); got %v", f)
	}
	if f := ForgeForURL("https://notgithub.com/o/r.git", forges, nil); f != nil {
		t.Errorf("an unconfigured host must not match; got %v", f)
	}
}

func TestForgeForURL_ResolvesVars(t *testing.T) {
	forges := []ForgeConfig{{ID: "gl", Provider: "gitlab", URL: "https://{var:host}"}}
	vars := map[string]string{"host": "gitlab.example.com"}
	if f := ForgeForURL("https://gitlab.example.com/o/r.git", forges, vars); f == nil || f.ID != "gl" {
		t.Errorf("var-templated forge URL should resolve and match, got %v", f)
	}
}
