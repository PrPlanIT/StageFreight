package toolchain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryAuthEnv(t *testing.T) {
	t.Run("explicit DOCKER_CONFIG wins", func(t *testing.T) {
		t.Setenv("DOCKER_CONFIG", "/creds")
		t.Setenv("HOME", t.TempDir())
		got := RegistryAuthEnv()
		if len(got) != 1 || got[0] != "DOCKER_CONFIG=/creds" {
			t.Fatalf("got %v, want [DOCKER_CONFIG=/creds]", got)
		}
	})
	t.Run("home docker config after a login", func(t *testing.T) {
		home := t.TempDir()
		cfgDir := filepath.Join(home, ".docker")
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DOCKER_CONFIG", "")
		t.Setenv("HOME", home)
		got := RegistryAuthEnv()
		if len(got) != 1 || got[0] != "DOCKER_CONFIG="+cfgDir {
			t.Fatalf("got %v, want [DOCKER_CONFIG=%s]", got, cfgDir)
		}
	})
	t.Run("no credentials anywhere", func(t *testing.T) {
		t.Setenv("DOCKER_CONFIG", "")
		t.Setenv("HOME", t.TempDir())
		if got := RegistryAuthEnv(); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})
}
