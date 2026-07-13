package modules

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func runYAML(t *testing.T, content string) []lint.Finding {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := (&yamlModule{}).Check(context.Background(), lint.FileInfo{Path: "f.yaml", AbsPath: p})
	if err != nil {
		t.Fatal(err)
	}
	return findings
}

// The regression that motivated this: Helm chart templates are not literal YAML,
// so a raw parse reports a CRIT "parse error" on every one. They must stay clean.
func TestYAML_HelmTemplateNotFlagged(t *testing.T) {
	tmpl := "apiVersion: v1\n" +
		"kind: ConfigMap\n" +
		"metadata:\n" +
		"  name: {{ .Release.Name }}-config\n" +
		"  labels:\n" +
		"{{- if .Values.extraLabels }}\n" +
		"    app: {{ .Values.app }}\n" +
		"{{- end }}\n" +
		"data:\n" +
		"  key: {{ .Values.value | quote }}\n"
	if f := runYAML(t, tmpl); len(f) != 0 {
		t.Fatalf("helm template flagged: %+v", f)
	}
}

// A block-rendering template whose neutralized skeleton still won't parse (a
// range that mixes a sequence with a root mapping) must stay silent, not emit a
// false-positive parse error.
func TestYAML_TemplateDynamicStaysSilent(t *testing.T) {
	tmpl := "{{ range .items }}\n" +
		"- name: {{ .name }}\n" +
		"  value: {{ .value }}\n" +
		"{{ end }}\n" +
		"extra: {{ .x }}\n"
	if f := runYAML(t, tmpl); len(f) != 0 {
		t.Fatalf("dynamic template flagged: %+v", f)
	}
}

// Skeleton validation still works through templates: a genuine static duplicate
// key in a templated file is still reported.
func TestYAML_TemplatedDuplicateKeyStillCaught(t *testing.T) {
	tmpl := "metadata:\n" +
		"  name: {{ .name }}\n" +
		"  name: other\n"
	f := runYAML(t, tmpl)
	if len(f) != 1 || f[0].Severity != lint.SeverityWarning {
		t.Fatalf("want 1 duplicate-key warning, got %+v", f)
	}
}

// Non-templated YAML is unchanged: real syntax errors are still CRIT.
func TestYAML_PlainSyntaxErrorStillFlagged(t *testing.T) {
	f := runYAML(t, "foo: [unclosed\n")
	if len(f) != 1 || f[0].Severity != lint.SeverityCritical {
		t.Fatalf("want 1 critical parse error, got %+v", f)
	}
}

func TestYAML_PlainValidClean(t *testing.T) {
	if f := runYAML(t, "a: 1\nb:\n  - x\n  - y\n"); len(f) != 0 {
		t.Fatalf("valid yaml flagged: %+v", f)
	}
}
