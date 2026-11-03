package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// fakeDockerCLI puts a logging `docker` on PATH whose remote bootstrap fails unless
// remoteOK; a docker-container builder bootstraps. Returns the log path.
func fakeDockerCLI(t *testing.T, remoteOK bool) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	state := filepath.Join(dir, "driver")
	remote := "1"
	if remoteOK {
		remote = "0"
	}
	script := `#!/bin/sh
echo "$*" >> "` + logPath + `"
case "$*" in
  "buildx create"*"--driver remote"*) echo remote > "` + state + `"; exit 0 ;;
  "buildx create"*) echo container > "` + state + `"; exit 0 ;;
  "buildx inspect --bootstrap"*)
    d=$(cat "` + state + `" 2>/dev/null)
    if [ "$d" = remote ]; then
      [ ` + remote + ` = 0 ] && exit 0
      echo "ERROR: failed to dial tcp buildkitd:1234: connection refused"; exit 1
    fi
    [ "$d" = container ] && exit 0
    echo "ERROR: no builder"; exit 1 ;;
  "context create"*) exit 0 ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func buildkitCerts(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"ca.pem", "cert.pem", "key.pem"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("BUILDKIT_CERT_PATH", dir)
}

func callLog(t *testing.T, path string) string {
	t.Helper()
	b, _ := os.ReadFile(path)
	return string(b)
}

// Without a daemon, a failed remote bootstrap is reported with its own error and no
// docker-container builder is attempted.
func TestEnsureBuilder_RemoteFailsWithoutDaemon_IsTerminal(t *testing.T) {
	log := fakeDockerCLI(t, false)
	buildkitCerts(t)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())                         // the builder record is written under ./.stagefreight
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1") // nothing answers: no daemon
	t.Setenv("DOCKER_CERT_PATH", "")

	info := EnsureBuilderWithBackend(config.BuilderConfig{}, &Backend{Kind: "buildkitd", Endpoint: "tcp://buildkitd:1234", Healthy: true})
	err := info.Err()
	if err == nil {
		t.Fatal("expected the remote builder failure, got a usable builder")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error %q should carry buildkitd's own failure", err)
	}
	if strings.Contains(callLog(t, log), "--driver docker-container") {
		t.Errorf("a docker-container builder was attempted without a daemon:\n%s", callLog(t, log))
	}
}

// With a daemon, a failed remote bootstrap falls back to a docker-container builder.
func TestEnsureBuilder_RemoteFailsWithDaemon_FallsBack(t *testing.T) {
	log := fakeDockerCLI(t, false)
	buildkitCerts(t)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())                      // the builder record is written under ./.stagefreight
	t.Setenv("DOCKER_HOST", backendListen(t)) // a daemon answers
	t.Setenv("DOCKER_CERT_PATH", t.TempDir())

	info := EnsureBuilderWithBackend(config.BuilderConfig{}, &Backend{Kind: "buildkitd", Endpoint: "tcp://buildkitd:1234", Healthy: true})
	if err := info.Err(); err != nil {
		t.Fatalf("expected the docker-container fallback to bootstrap, got %v", err)
	}
	if !strings.Contains(callLog(t, log), "--driver docker-container") {
		t.Errorf("expected a docker-container builder, calls:\n%s", callLog(t, log))
	}
}

// A healthy remote buildkitd is used as is.
func TestEnsureBuilder_RemoteHealthy(t *testing.T) {
	log := fakeDockerCLI(t, true)
	buildkitCerts(t)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir()) // the builder record is written under ./.stagefreight
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	t.Setenv("DOCKER_CERT_PATH", "")

	info := EnsureBuilderWithBackend(config.BuilderConfig{}, &Backend{Kind: "buildkitd", Endpoint: "tcp://buildkitd:1234", Healthy: true})
	if err := info.Err(); err != nil {
		t.Fatalf("remote builder should bootstrap: %v", err)
	}
	if info.Driver != "remote" {
		t.Errorf("driver = %q, want remote", info.Driver)
	}
	if strings.Contains(callLog(t, log), "--driver docker-container") {
		t.Errorf("no docker-container builder should be created:\n%s", callLog(t, log))
	}
}

func TestBuilderInfoErr(t *testing.T) {
	if err := (BuilderInfo{Name: "sf-builder", BootstrapOK: true}).Err(); err != nil {
		t.Fatalf("bootstrapped builder: %v", err)
	}
	err := (BuilderInfo{Name: "sf-builder", Status: "remote builder creation failed", RawOutput: "boom\n"}).Err()
	if err == nil || err.Error() != "buildx builder sf-builder: remote builder creation failed: boom" {
		t.Fatalf("err = %v", err)
	}
	if err := (BuilderInfo{Name: "sf-builder"}).Err(); err == nil || err.Error() != "buildx builder sf-builder: bootstrap failed" {
		t.Fatalf("err = %v", err)
	}
}
