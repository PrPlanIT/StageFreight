package docsgen

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// These tests are the docs-freshness gate: they fail in audition (per commit) when the
// hand-written docs drift from the generated schema. They run on the COMMITTED fragments,
// so a schema change that isn't reflected in docs/ breaks the build — which mkdocs --strict
// can't do (pymdownx.snippets renders a missing section EMPTY, silently).

func repoRootPath(t *testing.T) string { t.Helper(); return filepath.Join("..", "..") }

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRootPath(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

var startAnchorRe = regexp.MustCompile(`\[start:([a-z0-9_-]+)\]`)

func sectionAnchors(fragment string) map[string]bool {
	out := map[string]bool{}
	for _, m := range startAnchorRe.FindAllStringSubmatch(fragment, -1) {
		out[m[1]] = true
	}
	return out
}

// TestDocsFreshness_Coverage asserts every top-level Config yaml key has a generated
// config-reference section. Catches a schema key added/renamed without the committed
// fragment being regenerated.
func TestDocsFreshness_Coverage(t *testing.T) {
	anchors := sectionAnchors(readRepoFile(t, "docs/assets/modules/config-reference.md"))
	typ := reflect.TypeOf(config.Config{})
	for i := 0; i < typ.NumField(); i++ {
		key := strings.Split(typ.Field(i).Tag.Get("yaml"), ",")[0]
		if key == "" || key == "-" {
			continue
		}
		if !anchors[key] {
			t.Errorf("config key %q has no [start:%s] section in config-reference.md — regenerate the reference (stagefreight docs generate)", key, key)
		}
	}
}

// TestDocsFreshness_Wiring asserts every --8<-- snippet anchor referenced anywhere in docs/
// resolves to a real section in the generated fragments. This is the load-bearing check: a
// dead anchor renders empty under mkdocs --strict without erroring, so only a test catches it.
func TestDocsFreshness_Wiring(t *testing.T) {
	frags := map[string]map[string]bool{
		"config-reference": sectionAnchors(readRepoFile(t, "docs/assets/modules/config-reference.md")),
		"cli-reference":    sectionAnchors(readRepoFile(t, "docs/assets/modules/cli-reference.md")),
	}
	ref := regexp.MustCompile(`(config-reference|cli-reference)\.md:([a-z0-9_-]+)`)
	docsDir := filepath.Join(repoRootPath(t), "docs")
	err := filepath.Walk(docsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		// Skip the generated fragments themselves.
		if strings.Contains(path, filepath.Join("assets", "modules")) {
			return nil
		}
		b, _ := os.ReadFile(path)
		for _, m := range ref.FindAllStringSubmatch(string(b), -1) {
			if !frags[m[1]][m[2]] {
				rel, _ := filepath.Rel(repoRootPath(t), path)
				t.Errorf("%s: --8<-- %s.md:%s references a section that does not exist in the generated fragment", rel, m[1], m[2])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
