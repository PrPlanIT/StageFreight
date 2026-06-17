package auditionproof

import (
	"testing"
)

func TestReadMissingIsEmpty(t *testing.T) {
	r, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("Read missing: %v", err)
	}
	if r.FluxValidate != nil {
		t.Fatalf("expected empty results, got %+v", r)
	}
}

func TestWriteReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	in := &Results{
		FluxValidate: &FluxValidate{
			Roots: 3,
			Verdicts: map[string]Verdict{
				"flux-system/apps":    {Status: "pass"},
				"flux-system/gitlab":  {Status: "fail", Findings: []Finding{{Severity: "fail", Source: "core-schema", Message: "HelmRelease/gitlab: schema error"}}},
				"flux-system/storage": {Status: "fail", Findings: []Finding{{Severity: "fail", Source: "graph", Message: "in or downstream of a dependsOn cycle"}}},
			},
			NoSchema: map[string]int{"FooController": 2},
		},
	}
	if err := Write(dir, in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.FluxValidate == nil || out.FluxValidate.Roots != 3 {
		t.Fatalf("roundtrip lost data: %+v", out)
	}
	if out.FluxValidate.Verdicts["flux-system/gitlab"].Status != "fail" {
		t.Errorf("gitlab verdict not preserved: %+v", out.FluxValidate.Verdicts)
	}
	gl := out.FluxValidate.Verdicts["flux-system/gitlab"]
	if len(gl.Findings) != 1 || gl.Findings[0].Source != "core-schema" || gl.Findings[0].Severity != "fail" {
		t.Errorf("findings/provenance not preserved: %+v", gl)
	}
}
