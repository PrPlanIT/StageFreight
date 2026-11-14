package modules

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/lint"
)

func TestParseValidateJSON(t *testing.T) {
	out := []byte(`{"valid":false,"diagnostics":[
 {"severity":"error","summary":"Unsupported argument","detail":"An argument named \"x\" is not expected here.","range":{"filename":"main.tf","start":{"line":4}}},
 {"severity":"warning","summary":"Deprecated"}]}`)
	fs, err := parseValidateJSON("opentofu/site", out)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 {
		t.Fatalf("findings = %+v", fs)
	}
	if fs[0].File != "opentofu/site/main.tf" || fs[0].Line != 4 || fs[0].Severity != lint.SeverityWarning {
		t.Errorf("error diagnostic = %+v", fs[0])
	}
	if fs[1].File != "opentofu/site" || fs[1].Line != 1 || fs[1].Severity != lint.SeverityInfo {
		t.Errorf("warning diagnostic = %+v", fs[1])
	}
}

// TestOpenTofuModule_CheckAll: fmt sees only OpenTofu sources, validate runs per declared
// root, and a check that cannot run is one Warning, not a module error.
func TestOpenTofuModule_CheckAll(t *testing.T) {
	var fmtFiles []string
	var validated []string
	m := &openTofuModule{
		cfg: config.OpenTofuConfig{Backend: "terraform", Roots: config.OrderedOpenTofuRoots{{ID: "a", Path: "ot/a"}, {ID: "b", Path: "ot/b"}}},
		fmt: func(_ context.Context, tool string, _ map[string]config.ToolConstraint, _ string, files []string) ([]string, error) {
			fmtFiles = files
			return []string{"ot/a/main.tf"}, nil
		},
		validate: func(_ context.Context, _ string, _ map[string]config.ToolConstraint, _, rootPath string) ([]byte, error) {
			validated = append(validated, rootPath)
			if rootPath == "ot/b" {
				return nil, errors.New("no lock file")
			}
			return []byte(`{"diagnostics":[]}`), nil
		},
	}
	fs, err := m.CheckAll(context.Background(), []lint.FileInfo{{Path: "ot/a/main.tf"}, {Path: "ot/a/prod.tfvars"}, {Path: "README.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fmtFiles, []string{"ot/a/main.tf", "ot/a/prod.tfvars"}) {
		t.Errorf("fmt files = %v", fmtFiles)
	}
	if !slices.Equal(validated, []string{"ot/a", "ot/b"}) {
		t.Errorf("validated = %v", validated)
	}
	if len(fs) != 2 || fs[0].File != "ot/a/main.tf" || fs[1].Severity != lint.SeverityWarning {
		t.Errorf("findings = %+v", fs)
	}

	if fs, _ := (&openTofuModule{}).CheckAll(context.Background(), nil); fs != nil {
		t.Error("no declared roots must be inert")
	}
}
