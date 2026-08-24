package docker

import (
	"os"
	"path/filepath"
	"testing"
)

// The apk inventory must reflect the SHIPPED image, not the build. Packages installed under
// a `--virtual` group and then `apk del`'d are build-only and must not appear; nor may the
// virtual group name itself (".build-deps") or an unresolved variable ($PHPIZE_DEPS) be
// mistaken for a package. Runtime packages from a plain `apk add` still appear. Regression:
// the contents-apk badge rendered ".build-deps", ".plugins", "$PHPIZE_DEPS", "composer", …
func TestExtractInventory_VirtualAndDeletedApkExcluded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	const body = `FROM alpine:3.23.5
RUN set -eux; \
    apk add --no-cache nginx tini c-client; \
    apk add --no-cache --virtual .build-deps \
        $PHPIZE_DEPS freetype-dev libpng-dev; \
    docker-php-ext-install gd; \
    apk del .build-deps
RUN apk add --no-cache --virtual .plugins git composer && make hydrate && apk del .plugins
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

	// Runtime packages survive.
	for _, want := range []string{"nginx", "tini", "c-client"} {
		if !got[want] {
			t.Errorf("runtime apk package %q should be inventoried; got %v", want, got)
		}
	}
	// Build-only deps (under a deleted --virtual group), the group names, and the unresolved
	// variable must all be gone.
	for _, bad := range []string{".build-deps", ".plugins", "$PHPIZE_DEPS", "PHPIZE_DEPS", "freetype-dev", "libpng-dev", "git", "composer"} {
		if got[bad] {
			t.Errorf("apk inventory must not contain %q (build-only / virtual / variable); got %v", bad, got)
		}
	}
}
