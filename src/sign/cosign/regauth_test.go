package cosign

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Credentials are keyed as cosign looks the host up, and only for that host.
func TestWriteRegistryAuth(t *testing.T) {
	cases := map[string]string{
		"cr.optcp.com":        "cr.optcp.com",
		"docker.io":           "https://index.docker.io/v1/",
		"ghcr.io":             "ghcr.io",
		"registry.local:5000": "registry.local:5000",
	}
	for host, wantKey := range cases {
		t.Run(host, func(t *testing.T) {
			dir, cleanup, err := WriteRegistryAuth(host, "robot", "s3cret")
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(dir, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			var cfg struct {
				Auths map[string]struct {
					Auth string `json:"auth"`
				} `json:"auths"`
			}
			if err := json.Unmarshal(data, &cfg); err != nil {
				t.Fatal(err)
			}
			if len(cfg.Auths) != 1 {
				t.Fatalf("auths = %v, want exactly one registry", cfg.Auths)
			}
			got, ok := cfg.Auths[wantKey]
			if !ok {
				t.Fatalf("auths keys = %v, want %q", cfg.Auths, wantKey)
			}
			if dec, _ := base64.StdEncoding.DecodeString(got.Auth); string(dec) != "robot:s3cret" {
				t.Fatalf("auth = %q", dec)
			}
			if info, _ := os.Stat(filepath.Join(dir, "config.json")); info.Mode().Perm() != 0o600 {
				t.Errorf("config.json mode = %v, want 0600", info.Mode().Perm())
			}
			cleanup()
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("cleanup must remove the credentials")
			}
		})
	}
}

// The explicit DOCKER_CONFIG overrides the ambient one; an empty dir changes nothing.
func TestWithRegistryAuth(t *testing.T) {
	base := []string{"HOME=/tmp", "DOCKER_CONFIG=/ambient"}
	if got := withRegistryAuth(base, ""); strings.Join(got, ",") != strings.Join(base, ",") {
		t.Fatalf("empty dir changed env: %v", got)
	}
	got := withRegistryAuth(base, "/creds")
	if got[len(got)-1] != "DOCKER_CONFIG=/creds" {
		t.Fatalf("explicit registry auth must be the last DOCKER_CONFIG, got %v", got)
	}
}
