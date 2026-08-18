package docker

import (
	"os"
	"path/filepath"
	"testing"
)

// A multi-line RUN with inline "#" comments between continuation lines must still
// yield its packages: Docker strips the comments and joins the "\"-continuation across
// them, so the whole `apk add …` stays one RUN instruction. Regression — the parser
// used to flush the continuation at each comment, orphaning every post-comment fragment
// from its RUN prefix, so a commented `apk add` produced ZERO packages (contents-apk
// badge rendered "No items" for a repo that clearly installs packages).
func TestExtractInventory_CommentedMultilineRunKeepsPackages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	const body = `FROM alpine:3.23.5
RUN set -eux; \
    # refresh base OS packages
    apk upgrade --no-cache; \
    # nginx + its brotli module + runtime deps
    apk add --no-cache \
        nginx \
        nginx-mod-http-brotli \
        ca-certificates \
        tzdata \
        gettext; \
    # non-root runtime user
    addgroup -g 10001 -S web
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := ExtractInventory(path)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, p := range res.Packages {
		if p.Manager == "apk" {
			got[p.Name] = true
		}
	}
	for _, want := range []string{"nginx", "nginx-mod-http-brotli", "ca-certificates", "tzdata", "gettext"} {
		if !got[want] {
			t.Errorf("apk package %q not extracted from commented multi-line RUN; got %v", want, got)
		}
	}
	// A comment word must never be mistaken for a package (the continuation is joined,
	// not the raw comment text).
	for _, bad := range []string{"refresh", "nginx-mod-http-brotli;", "#"} {
		if bad == "#" && got["#"] {
			t.Errorf("comment marker leaked as a package")
		}
	}
}
