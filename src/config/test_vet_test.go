package config

import (
	"strings"
	"testing"
)

func TestValidate_TestSuiteVet(t *testing.T) {
	cases := []struct {
		name    string
		suite   TestSuite
		wantErr string // "" = no vet error
	}{
		{"all on go", TestSuite{ID: "unit", Tool: TestToolGo, Vet: "all"}, ""},
		{"off on go", TestSuite{ID: "unit", Tool: TestToolGo, Vet: "off"}, ""},
		{"unset", TestSuite{ID: "unit", Tool: TestToolGo}, ""},
		{"unknown value", TestSuite{ID: "unit", Tool: TestToolGo, Vet: "strict"}, `unknown vet "strict"`},
		{"not a go suite", TestSuite{ID: "unit", Tool: TestToolRust, Vet: "all"}, `vet applies to tool go`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{}
			cfg.Test.Suites = []TestSuite{c.suite}
			_, err := Validate(cfg)
			got := ""
			if err != nil {
				got = err.Error()
			}
			hasVetErr := strings.Contains(got, "vet")
			switch {
			case c.wantErr == "" && hasVetErr:
				t.Fatalf("unexpected vet error: %s", got)
			case c.wantErr != "" && !strings.Contains(got, c.wantErr):
				t.Fatalf("error %q, want it to contain %q", got, c.wantErr)
			}
		})
	}
}
