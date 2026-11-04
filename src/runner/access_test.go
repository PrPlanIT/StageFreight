package runner

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A docker socket or BuildKit client key that exists but is closed to this uid is
// named, with the uid, in the finding — not reported as an absent daemon.
func TestAccessDenied_NamesClosedEndpoints(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	dir := t.TempDir()
	sock := filepath.Join(dir, "docker.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	if err := os.Chmod(sock, 0o000); err != nil {
		t.Fatal(err)
	}

	certs := t.TempDir()
	for _, f := range buildKitCertFiles {
		if err := os.WriteFile(filepath.Join(certs, f), []byte("pem"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(certs, "key.pem")
	if err := os.Chmod(key, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUILDKIT_CERT_PATH", certs)

	if BuildKitCertsPresent(certs) {
		t.Error("an unreadable key must not count as a usable PKI")
	}
	denied := accessDenied(sock)
	if strings.Join(denied, ",") != sock+","+key {
		t.Fatalf("denied = %v, want the socket and the key", denied)
	}
	detail := withAccessDenied("docker socket not available", denied)
	for _, want := range []string{sock, key, "uid "} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q should mention %q", detail, want)
		}
	}
}

// Endpoints this uid can use, or that don't exist, aren't reported.
func TestAccessDenied_OpenOrAbsentIsQuiet(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "docker.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	t.Setenv("BUILDKIT_CERT_PATH", filepath.Join(dir, "none"))
	if denied := accessDenied(sock); len(denied) != 0 {
		t.Fatalf("denied = %v, want none", denied)
	}
	if got := withAccessDenied("d", nil); got != "d" {
		t.Errorf("detail = %q, want it unchanged", got)
	}
}
