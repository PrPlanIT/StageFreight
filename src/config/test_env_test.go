package config

import (
	"strings"
	"testing"
)

func TestValidate_TestSuiteEnv(t *testing.T) {
	cases := []struct {
		name    string
		suite   TestSuite
		wantErr string // "" = no env error
	}{
		{"names", TestSuite{ID: "unit", Tool: TestToolGo, Env: []string{"SF_SIM_TOKEN", "_X1"}}, ""},
		{"not a name", TestSuite{ID: "unit", Tool: TestToolGo, Env: []string{"SF-SIM"}}, `"SF-SIM" is not an environment variable name`},
		{"a value, not a name", TestSuite{ID: "unit", Tool: TestToolGo, Env: []string{"TOKEN=abc"}}, "not an environment variable name"},
		{"runner-owned", TestSuite{ID: "unit", Tool: TestToolGo, Env: []string{"PATH"}}, "set by the test runner itself"},
		{"script", TestSuite{ID: "s", Tool: TestToolScript, Command: "true", Env: []string{"X"}}, "already runs with the full environment"},
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
			hasEnvErr := strings.Contains(got, ".env:")
			switch {
			case c.wantErr == "" && hasEnvErr:
				t.Fatalf("unexpected env error: %s", got)
			case c.wantErr != "" && !strings.Contains(got, c.wantErr):
				t.Fatalf("error %q, want it to contain %q", got, c.wantErr)
			}
		})
	}
}
