package config

import (
	"strings"
	"testing"
)

// A secret path is mounted into the build, so one that escapes the repo must be refused
// at load — not discovered afterwards by looking at what got mounted.
func TestSecretPathsMustStayInsideTheRepo(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"absolute", "/etc/passwd"},
		{"traversal", "../../.ssh/id_rsa"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaults()
			cfg.Builds = []BuildConfig{{ID: "image", Kind: "docker", Secrets: map[string]string{"k": tc.path}}}
			_, err := Validate(cfg)
			if err == nil || !strings.Contains(err.Error(), "inside the repo") {
				t.Fatalf("err = %v, want a refusal naming the repo boundary", err)
			}
		})
	}

	t.Run("a path inside the repo is accepted", func(t *testing.T) {
		cfg := defaults()
		cfg.Builds = []BuildConfig{{ID: "image", Kind: "docker", Secrets: map[string]string{"apps_json": "apps.json"}}}
		if _, err := Validate(cfg); err != nil {
			t.Fatalf("in-repo secret path rejected: %v", err)
		}
	})
}
