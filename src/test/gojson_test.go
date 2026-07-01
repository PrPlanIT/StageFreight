package test

import (
	"strings"
	"testing"
)

func TestParseGoCoverage(t *testing.T) {
	cases := map[string]float64{
		"coverage: 73.2% of statements\n": 73.2,
		"coverage: 100.0% of statements":  100.0,
		"coverage: 0.0% of statements":    0.0,
	}
	for in, want := range cases {
		if got, ok := parseGoCoverage(in); !ok || got != want {
			t.Errorf("parseGoCoverage(%q) = %v,%v; want %v,true", in, got, ok, want)
		}
	}
	if _, ok := parseGoCoverage("PASS\n"); ok {
		t.Error("a line without coverage should return ok=false")
	}
}

// go test -json emits the coverage as a package-level output event under -cover.
func TestParseGoTest_Coverage(t *testing.T) {
	const stream = `{"Action":"pass","Package":"x/foo","Test":"TestA","Elapsed":0}
{"Action":"output","Package":"x/foo","Output":"coverage: 73.2% of statements\n"}
{"Action":"pass","Package":"x/foo","Elapsed":0.01}
`
	pkgs := parseGoTest(strings.NewReader(stream), "x", nil, nil)
	if len(pkgs) != 1 {
		t.Fatalf("want 1 package, got %d", len(pkgs))
	}
	if pkgs[0].Coverage != 73.2 {
		t.Errorf("coverage = %v, want 73.2", pkgs[0].Coverage)
	}
	if pkgs[0].Status != StatusPassed || pkgs[0].Tests != 1 {
		t.Errorf("pkg = %+v", pkgs[0])
	}
}

// Without -cover there is no coverage line, so Coverage stays "not measured" (<0).
func TestParseGoTest_NoCoverage(t *testing.T) {
	const stream = `{"Action":"pass","Package":"x/foo","Test":"TestA","Elapsed":0}
{"Action":"pass","Package":"x/foo","Elapsed":0.01}
`
	pkgs := parseGoTest(strings.NewReader(stream), "x", nil, nil)
	if len(pkgs) != 1 || pkgs[0].Coverage >= 0 {
		t.Errorf("want 1 pkg with Coverage<0, got %+v", pkgs)
	}
}
