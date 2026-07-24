package config

import (
	"strings"
	"testing"
)

// TestValidateTarget_Pages covers kind-specific validation for pages targets:
// build XOR dir, provider requirement, versioning modes, and — the focus here —
// the Cloudflare project-name rule (project:, else the target id), which must fail
// at config load rather than opaquely at deploy/create time.
func TestValidateTarget_Pages(t *testing.T) {
	base := func() TargetConfig {
		return TargetConfig{
			ID:       "docs",
			Kind:     "pages",
			Provider: "cloudflare",
			Dir:      "./www-data",
		}
	}
	check := func(name string, mutate func(*TargetConfig), wantSubstr string) {
		t.Run(name, func(t *testing.T) {
			tc := base()
			mutate(&tc)
			errs := validateTarget(tc, "targets[docs]", map[string]bool{"site": true}, nil)
			joined := strings.Join(errs, "; ")
			if wantSubstr == "" {
				if len(errs) != 0 {
					t.Fatalf("expected no errors, got: %s", joined)
				}
				return
			}
			if !strings.Contains(joined, wantSubstr) {
				t.Fatalf("expected error containing %q, got: %s", wantSubstr, joined)
			}
		})
	}

	check("valid dir + cloudflare", func(tc *TargetConfig) {}, "")
	check("valid github", func(tc *TargetConfig) { tc.Provider = "github" }, "")
	check("build XOR dir — both set", func(tc *TargetConfig) { tc.Build = "site" }, "exactly one of build or dir")
	check("build XOR dir — neither set", func(tc *TargetConfig) { tc.Dir = "" }, "exactly one of build or dir")
	check("provider required", func(tc *TargetConfig) { tc.Provider = "" }, "requires provider")
	check("versioning keep reserved", func(tc *TargetConfig) { tc.Versioning = &PagesVersioning{Mode: "keep"} }, "not yet implemented")

	// Cloudflare project-name rule — effective name is project: else the target id.
	check("id used as project (valid)", func(tc *TargetConfig) {}, "")
	check("uppercase target id rejected", func(tc *TargetConfig) { tc.ID = "Docs" }, "project name")
	check("underscore in id rejected", func(tc *TargetConfig) { tc.ID = "my_docs" }, "project name")
	check("leading hyphen rejected", func(tc *TargetConfig) { tc.Project = "-docs" }, "project name")
	check("trailing hyphen rejected", func(tc *TargetConfig) { tc.Project = "docs-" }, "project name")
	check("explicit project overrides bad id", func(tc *TargetConfig) { tc.ID = "My_Docs"; tc.Project = "my-docs" }, "")
	check("single char project valid", func(tc *TargetConfig) { tc.Project = "a" }, "")
	check("too long (59 chars) rejected", func(tc *TargetConfig) { tc.Project = strings.Repeat("a", 59) }, "project name")
	check("max length (58 chars) valid", func(tc *TargetConfig) { tc.Project = strings.Repeat("a", 58) }, "")
	// A github target with an id CF would reject must NOT trip the CF rule.
	check("github ignores cf name rule", func(tc *TargetConfig) { tc.Provider = "github"; tc.ID = "My_Docs" }, "")

	// Domain: Cloudflare attaches every listed domain; GitHub serves one CNAME, so a
	// list is rejected at load rather than silently truncated to the first entry.
	check("cloudflare single domain valid", func(tc *TargetConfig) { tc.Domain = StringOrList{"a.com"} }, "")
	check("cloudflare multi domain valid", func(tc *TargetConfig) { tc.Domain = StringOrList{"a.com", "b.com"} }, "")
	check("github single domain valid", func(tc *TargetConfig) { tc.Provider = "github"; tc.Domain = StringOrList{"a.com"} }, "")
	check("github multi domain rejected", func(tc *TargetConfig) { tc.Provider = "github"; tc.Domain = StringOrList{"a.com", "b.com"} }, "supports a single custom domain")
}
