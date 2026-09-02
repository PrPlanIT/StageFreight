package security

import (
	"strings"
	"testing"
)

// TestBuildBodies_NeutralizeScannerText pins the mdText contract on both body
// builders: scanner-supplied text renders as literal text. A CVE description
// quoting a <script> tag must not reach the document as live HTML (an unclosed
// script element makes an HTML sanitizer swallow everything after it — the
// whole tail of the release notes), pipes must not add table columns, and
// newlines must not split rows.
func TestBuildBodies_NeutralizeScannerText(t *testing.T) {
	result := &ScanResult{
		Critical: 1,
		High:     1,
		Vulnerabilities: []Vulnerability{
			{ID: "CVE-2026-39826", Severity: "CRITICAL", Package: "stdlib", Installed: "go1.25.8", FixedIn: "1.25.10",
				Description: "If a trusted template author were to write a <script> tag"},
			{ID: "GHSA-x|y", Severity: "HIGH", Package: "demo", Installed: "1.0",
				Description: "first line\nsecond line"},
		},
	}

	for name, body := range map[string]string{
		"full":     buildFullBody(result, "tile", 0),
		"detailed": buildDetailedBody(result, "tile"),
	} {
		if strings.Contains(body, "<script>") {
			t.Errorf("%s: raw <script> reached the body:\n%s", name, body)
		}
		if !strings.Contains(body, "&lt;script&gt;") {
			t.Errorf("%s: tag-shaped text must survive as entities:\n%s", name, body)
		}
		if strings.Contains(body, "GHSA-x|y") {
			t.Errorf("%s: unescaped pipe in scanner text:\n%s", name, body)
		}
		if !strings.Contains(body, `GHSA-x\|y`) {
			t.Errorf("%s: pipe must be backslash-escaped:\n%s", name, body)
		}
		if strings.Contains(body, "first line\nsecond line") {
			t.Errorf("%s: newline in scanner text must collapse to a space:\n%s", name, body)
		}
	}
}
