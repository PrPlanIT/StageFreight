package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestCommandSpec_ScalarAndList pins that `command:` accepts either a scalar shell
// string or an argv sequence (shell-joined, quoting parts with spaces).
func TestCommandSpec_ScalarAndList(t *testing.T) {
	cases := []struct {
		name string
		yml  string
		want string
	}{
		{"scalar", `command: "go build"`, "go build"},
		{"list", `command: [go, run, ./..., docs, generate]`, "go run ./... docs generate"},
		{"list quotes spaces", `command: [echo, "a b"]`, "echo 'a b'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s struct {
				Command CommandSpec `yaml:"command"`
			}
			if err := yaml.Unmarshal([]byte(tc.yml), &s); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if string(s.Command) != tc.want {
				t.Errorf("Command = %q, want %q", s.Command, tc.want)
			}
		})
	}
}

// TestValidate_KindCommand covers the kind: command validation: an explicit command +
// ≥1 typed repo-relative output; no builder.
func TestValidate_KindCommand(t *testing.T) {
	base := func() BuildConfig {
		return BuildConfig{
			ID:      "reference",
			Kind:    "command",
			Command: "stagefreight docs generate --output docs/generated",
			Outputs: []OutputSpec{{Type: "tree", Source: "docs/generated"}},
		}
	}
	// errStr returns the full validation error text (Version:1 avoids the unrelated
	// version error); command-validation messages are distinctive enough to match on.
	errStr := func(b BuildConfig) string {
		_, err := Validate(&Config{Version: 1, Builds: []BuildConfig{b}})
		if err == nil {
			return ""
		}
		return err.Error()
	}

	// The valid build must produce none of the kind-command validation errors.
	for _, marker := range []string{"kind command requires", "outputs[", "not valid for kind command"} {
		if strings.Contains(errStr(base()), marker) {
			t.Errorf("valid kind command tripped %q: %s", marker, errStr(base()))
		}
	}

	check := func(name string, mutate func(*BuildConfig), want string) {
		t.Run(name, func(t *testing.T) {
			b := base()
			mutate(&b)
			if e := errStr(b); !strings.Contains(e, want) {
				t.Errorf("want error containing %q, got: %s", want, e)
			}
		})
	}
	check("missing command", func(b *BuildConfig) { b.Command = "" }, "requires command")
	check("no outputs", func(b *BuildConfig) { b.Outputs = nil }, "requires at least one output")
	check("bad output type", func(b *BuildConfig) { b.Outputs[0].Type = "image" }, "type \"image\" is invalid")
	check("absolute source", func(b *BuildConfig) { b.Outputs[0].Source = "/etc/x" }, "must be repo-relative")
	check("escaping source", func(b *BuildConfig) { b.Outputs[0].Source = "../x" }, "must be repo-relative")
	check("builder rejected", func(b *BuildConfig) { b.Builder = "go" }, "builder is not valid for kind command")
}
