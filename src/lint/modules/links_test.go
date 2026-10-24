package modules

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// runLinks writes files into a temp repo, chdirs there (CheckAll resolves against the working
// dir), and returns the module's findings. External checks are stubbed off unless a test
// overrides externalCheck.
func runLinks(t *testing.T, mod *linksModule, files map[string]string) []lint.Finding {
	t.Helper()
	dir := t.TempDir()
	var infos []lint.FileInfo
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		infos = append(infos, lint.FileInfo{Path: rel, AbsPath: abs, Content: lint.Content{Kind: lint.ContentText}})
	}
	oldwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	findings, err := mod.CheckAll(context.Background(), infos)
	if err != nil {
		t.Fatalf("CheckAll: %v", err)
	}
	return findings
}

func newLinks(t *testing.T, opts map[string]any) *linksModule {
	t.Helper()
	m := &linksModule{cfg: defaultLinksConfig()}
	if err := m.Configure(opts); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	return m
}

func TestLinks_OptInByDefault(t *testing.T) {
	m := &linksModule{}
	if m.DefaultEnabled() {
		t.Fatal("links must be opt-in (DefaultEnabled=false)")
	}
	if m.AutoDetect() != nil {
		t.Fatal("links must not auto-enable (AutoDetect=nil)")
	}
}

func TestLinks_CheckIsMisdispatchGuard(t *testing.T) {
	if _, err := (&linksModule{}).Check(context.Background(), lint.FileInfo{Path: "a.md"}); err == nil {
		t.Fatal("Check must error on a whole-repo module")
	}
}

func TestLinks_InternalResolvesAndExtensionless(t *testing.T) {
	f := runLinks(t, newLinks(t, nil), map[string]string{
		"a.md":         "[x](/b.md) [y](/docs/page) [z](docs/page.md)\n",
		"b.md":         "ok\n",
		"docs/page.md": "ok\n",
	})
	if len(f) != 0 {
		t.Fatalf("expected all internal links to resolve, got: %+v", f)
	}
}

func TestLinks_InternalDanglingBlocks(t *testing.T) {
	f := runLinks(t, newLinks(t, map[string]any{"internal": "error"}), map[string]string{"a.md": "[gone](/missing)\n"})
	if len(f) != 1 || f[0].RuleID != "unresolved-link" || !f[0].Blocks() {
		t.Fatalf("want 1 blocking unresolved-link, got: %+v", f)
	}
}

// TestLinks_DefaultSeverityIsWarn pins the warn-on-enable contract: with no per-class
// config, a dangling internal link reports but does not block, so turning the module on
// can never break a build until a class is deliberately escalated to error.
func TestLinks_DefaultSeverityIsWarn(t *testing.T) {
	f := runLinks(t, newLinks(t, nil), map[string]string{"a.md": "[gone](/missing)\n"})
	if len(f) != 1 || f[0].RuleID != "unresolved-link" || f[0].Severity != lint.SeverityWarning || f[0].Blocks() {
		t.Fatalf("default severity must be a non-blocking warn, got: %+v", f)
	}
}

func TestLinks_SectionSiblingDotMd(t *testing.T) {
	f := runLinks(t, newLinks(t, nil), map[string]string{
		"a.md":                      "see [it](/information-technology)\n",
		"information-technology.md": "# index\n",
	})
	if len(f) != 0 {
		t.Fatalf("section sibling .md should resolve, got: %+v", f)
	}
}

func TestLinks_RelativeAndParent(t *testing.T) {
	f := runLinks(t, newLinks(t, nil), map[string]string{
		"docs/a.md": "[sib](b.md) [up](../top.md)\n",
		"docs/b.md": "ok\n",
		"top.md":    "ok\n",
	})
	if len(f) != 0 {
		t.Fatalf("relative/parent links should resolve, got: %+v", f)
	}
}

func TestLinks_WikiRootConfig(t *testing.T) {
	// With root=content, an absolute /guide resolves to content/guide.md.
	f := runLinks(t, newLinks(t, map[string]any{"root": "content"}), map[string]string{
		"a.md":             "[g](/guide)\n",
		"content/guide.md": "ok\n",
	})
	if len(f) != 0 {
		t.Fatalf("absolute link should resolve against configured root, got: %+v", f)
	}
}

func TestLinks_FencedCodeIgnored(t *testing.T) {
	f := runLinks(t, newLinks(t, nil), map[string]string{
		"a.md": "text\n```\n[nope](/missing)\n```\nmore\n",
	})
	if len(f) != 0 {
		t.Fatalf("links inside fenced code must be ignored, got: %+v", f)
	}
}

func TestLinks_TitleAndAngleBrackets(t *testing.T) {
	f := runLinks(t, newLinks(t, nil), map[string]string{
		"a.md": "[t](/b.md \"a title\") [g](</b.md>)\n",
		"b.md": "ok\n",
	})
	if len(f) != 0 {
		t.Fatalf("title/angle-bracket forms should resolve, got: %+v", f)
	}
}

// --- anchors ---

func TestLinks_AnchorResolvesCrossFileAndSamePage(t *testing.T) {
	f := runLinks(t, newLinks(t, nil), map[string]string{
		"a.md": "[x](/b#my-heading) and [here](#top)\n# Top\n",
		"b.md": "## My Heading\n",
	})
	if len(f) != 0 {
		t.Fatalf("valid anchors should resolve, got: %+v", f)
	}
}

func TestLinks_AnchorMissingBlocks(t *testing.T) {
	f := runLinks(t, newLinks(t, map[string]any{"anchors": "error"}), map[string]string{
		"a.md": "[x](/b#nope)\n",
		"b.md": "## Real Heading\n",
	})
	if len(f) != 1 || f[0].RuleID != "unresolved-anchor" || !f[0].Blocks() {
		t.Fatalf("want 1 blocking unresolved-anchor, got: %+v", f)
	}
}

func TestLinks_AnchorExplicitIDAndHTML(t *testing.T) {
	f := runLinks(t, newLinks(t, nil), map[string]string{
		"a.md": "[x](/b#custom) [y](/b#manual)\n",
		"b.md": "## Heading {#custom}\n<a name=\"manual\"></a>\n",
	})
	if len(f) != 0 {
		t.Fatalf("explicit {#id} and html anchors should resolve, got: %+v", f)
	}
}

func TestLinks_AnchorsOffDisables(t *testing.T) {
	f := runLinks(t, newLinks(t, map[string]any{"anchors": "off"}), map[string]string{
		"a.md": "[x](/b#nope)\n",
		"b.md": "## Real\n",
	})
	if len(f) != 0 {
		t.Fatalf("anchors:off should suppress anchor findings, got: %+v", f)
	}
}

// --- external ---

// TestLinks_ExternalOffByDefault pins the safe default: with no external config, the module
// makes no network call and emits nothing — flipping links on never reaches the network.
func TestLinks_ExternalOffByDefault(t *testing.T) {
	orig := externalCheck
	t.Cleanup(func() { externalCheck = orig })
	called := false
	externalCheck = func(_ context.Context, _ string, _ time.Duration) (bool, string) {
		called = true
		return false, "HTTP 404"
	}
	f := runLinks(t, newLinks(t, nil), map[string]string{
		"a.md": "[bad](https://dead.example)\n",
	})
	if len(f) != 0 || called {
		t.Fatalf("external must be off by default (no finding, no network call), got findings %+v called=%v", f, called)
	}
}

func TestLinks_ExternalWarnWhenEnabled(t *testing.T) {
	orig := externalCheck
	t.Cleanup(func() { externalCheck = orig })
	externalCheck = func(_ context.Context, u string, _ time.Duration) (bool, string) {
		return u == "https://ok.example", "HTTP 404"
	}
	f := runLinks(t, newLinks(t, map[string]any{"external": "warn"}), map[string]string{
		"a.md": "[good](https://ok.example) [bad](https://dead.example)\n",
	})
	if len(f) != 1 {
		t.Fatalf("want 1 external finding, got: %+v", f)
	}
	if f[0].RuleID != "external-link" || f[0].Severity != lint.SeverityWarning || f[0].Blocks() {
		t.Fatalf("external:warn must be non-blocking warn, got: %+v", f[0])
	}
}

func TestLinks_ExternalErrorBlocks(t *testing.T) {
	orig := externalCheck
	t.Cleanup(func() { externalCheck = orig })
	externalCheck = func(_ context.Context, _ string, _ time.Duration) (bool, string) { return false, "HTTP 500" }
	f := runLinks(t, newLinks(t, map[string]any{"external": "error"}), map[string]string{
		"a.md": "[bad](https://dead.example)\n",
	})
	if len(f) != 1 || !f[0].Blocks() {
		t.Fatalf("external:error should block, got: %+v", f)
	}
}

func TestLinks_ExternalOffAndSkip(t *testing.T) {
	orig := externalCheck
	t.Cleanup(func() { externalCheck = orig })
	externalCheck = func(_ context.Context, _ string, _ time.Duration) (bool, string) { return false, "HTTP 404" }

	if f := runLinks(t, newLinks(t, map[string]any{"external": "off"}), map[string]string{
		"a.md": "[bad](https://dead.example)\n",
	}); len(f) != 0 {
		t.Fatalf("external:off must check nothing, got: %+v", f)
	}
	if f := runLinks(t, newLinks(t, map[string]any{"external_skip": []any{"https://skip.example/*"}}), map[string]string{
		"a.md": "[s](https://skip.example/x)\n",
	}); len(f) != 0 {
		t.Fatalf("external_skip glob must suppress, got: %+v", f)
	}
}

func TestLinks_MailtoSkipped(t *testing.T) {
	f := runLinks(t, newLinks(t, nil), map[string]string{"a.md": "[m](mailto:x@y.z)\n"})
	if len(f) != 0 {
		t.Fatalf("mailto must be skipped, got: %+v", f)
	}
}

// --- severity + scoping ---

func TestLinks_InternalWarnDoesNotBlock(t *testing.T) {
	f := runLinks(t, newLinks(t, map[string]any{"internal": "warn"}), map[string]string{"a.md": "[g](/missing)\n"})
	if len(f) != 1 || f[0].Severity != lint.SeverityWarning || f[0].Blocks() {
		t.Fatalf("internal:warn must warn, not block, got: %+v", f)
	}
}

func TestLinks_ScopingExcludeAndInclude(t *testing.T) {
	ex := runLinks(t, newLinks(t, map[string]any{"exclude": []any{"vendor/**"}}), map[string]string{
		"vendor/a.md": "[g](/missing)\n",
		"b.md":        "[g](/missing)\n",
	})
	if len(ex) != 1 || ex[0].File != "b.md" {
		t.Fatalf("exclude glob should scope out vendor/, got: %+v", ex)
	}
	in := runLinks(t, newLinks(t, map[string]any{"include": []any{"docs/**"}}), map[string]string{
		"docs/a.md": "[g](/missing)\n",
		"other.md":  "[g](/missing)\n",
	})
	if len(in) != 1 || in[0].File != "docs/a.md" {
		t.Fatalf("include glob should bound scope to docs/, got: %+v", in)
	}
}

func TestLinks_BadSeverityRejected(t *testing.T) {
	if err := (&linksModule{cfg: defaultLinksConfig()}).Configure(map[string]any{"internal": "loud"}); err == nil {
		t.Fatal("invalid severity must be rejected by Configure")
	}
}
