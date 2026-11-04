// Package testdocker gives tests a Docker daemon's services: a check that one is usable, and a
// throwaway OCI registry.
package testdocker

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// RegistryImage is the registry Registry runs.
const RegistryImage = "docker.io/library/registry:3.1.2"

// Available reports whether a Docker daemon is usable.
func Available() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	return exec.Command("docker", "info").Run() == nil
}

// Registry runs a registry on a port Docker assigns on localhost, so concurrent runs on one
// daemon never collide, waits until it serves, and removes it when the test ends. It
// returns the registry's host:port, and skips the test when no Docker daemon is usable.
func Registry(t *testing.T) string {
	t.Helper()
	if !Available() {
		t.Skip("docker not available — this test needs a real registry")
	}
	// Only stdout carries the container id; a first pull reports progress on stderr.
	var stderr strings.Builder
	run := exec.Command("docker", "run", "-d", "--rm", "-p", "127.0.0.1::5000", RegistryImage)
	run.Stderr = &stderr
	out, err := run.Output()
	if err != nil {
		t.Fatalf("start registry: %v\n%s", err, stderr.String())
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })

	port, err := exec.Command("docker", "port", id, "5000/tcp").Output()
	if err != nil {
		t.Fatalf("registry port: %v", err)
	}
	addr := strings.TrimSpace(strings.SplitN(string(port), "\n", 2)[0])

	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get(fmt.Sprintf("http://%s/v2/", addr))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return addr
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("registry at %s didn't serve within 30s: %v", addr, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
