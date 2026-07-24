package discovery

import "testing"

func TestEscapeModPath(t *testing.T) {
	// The Go module proxy case-encodes uppercase letters as "!"+lowercase; without it
	// these modules 404 and get silently reported "unresolved".
	cases := map[string]string{
		"github.com/Masterminds/semver/v3":   "github.com/!masterminds/semver/v3",
		"github.com/BobuSumisu/aho-corasick": "github.com/!bobu!sumisu/aho-corasick",
		"github.com/Azure/azure-sdk-for-go":  "github.com/!azure/azure-sdk-for-go",
		"dario.cat/mergo":                    "dario.cat/mergo",
		"github.com/go-git/go-git/v5":        "github.com/go-git/go-git/v5",
		"golang.org/x/mod":                   "golang.org/x/mod",
	}
	for in, want := range cases {
		if got := escapeModPath(in); got != want {
			t.Errorf("escapeModPath(%q) = %q, want %q", in, got, want)
		}
	}
}
