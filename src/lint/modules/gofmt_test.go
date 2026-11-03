package modules

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func gofmtCheck(t *testing.T, name, content string) ([]lint.Finding, string) {
	t.Helper()
	dir := t.TempDir()
	abs := filepath.Join(dir, name)
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := (&gofmtModule{}).Check(context.Background(), lint.FileInfo{Path: name, AbsPath: abs})
	if err != nil {
		t.Fatal(err)
	}
	return findings, dir
}

// An unformatted Go file is reported at its first differing line, and --fix-safe's
// applier turns it into exactly what gofmt would write.
func TestGofmt_ReportsAndFixes(t *testing.T) {
	src := "package x\n\nfunc F() int {\n\treturn 1\n}\n\nvar  y   = 2\n"
	findings, dir := gofmtCheck(t, "x.go", src)
	if len(findings) != 1 || findings[0].Line != 7 || findings[0].Fix == nil {
		t.Fatalf("findings = %+v, want one fixable finding at line 7", findings)
	}
	if _, err := lint.ApplyRemediations(findings, dir, map[string]bool{"gofmt": true}, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "x.go"))
	if want := "package x\n\nfunc F() int {\n\treturn 1\n}\n\nvar y = 2\n"; string(got) != want {
		t.Fatalf("fixed file:\n%s\nwant:\n%s", got, want)
	}
}

// Formatted files, non-Go files and unparsable Go (the compiler's to report) are quiet.
func TestGofmt_Quiet(t *testing.T) {
	for name, content := range map[string]string{
		"ok.go":     "package x\n\nvar y = 2\n",
		"notes.txt": "var  y   = 2\n",
		"broken.go": "package x\n\nfunc {\n",
	} {
		if findings, _ := gofmtCheck(t, name, content); len(findings) != 0 {
			t.Errorf("%s: unexpected findings %+v", name, findings)
		}
	}
}
