package docker

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// backendListen opens a TCP listener on localhost and returns its tcp:// endpoint.
func backendListen(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return "tcp://" + l.Addr().String()
}

// TestProbeBuildkit_UsesBuildkitHost pins that buildkitd is probed at the
// endpoint BUILDKIT_HOST names: a listener on an arbitrary host:port is found,
// and the endpoint is what the remote builder will be created against.
func TestProbeBuildkit_UsesBuildkitHost(t *testing.T) {
	certs := t.TempDir()
	if err := os.WriteFile(filepath.Join(certs, "ca.pem"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint := backendListen(t)
	t.Setenv("BUILDKIT_CERT_PATH", certs)
	t.Setenv("BUILDKIT_HOST", endpoint)

	bk := probeBuildkit()
	if bk == nil {
		t.Fatalf("probeBuildkit() = nil, want a backend at %s", endpoint)
	}
	if bk.Kind != "buildkitd" || bk.Endpoint != endpoint || bk.CertPath != certs || !bk.Healthy {
		t.Errorf("probeBuildkit() = %+v", *bk)
	}
}

func TestProbeBuildkit_NotReachable(t *testing.T) {
	certs := t.TempDir()
	if err := os.WriteFile(filepath.Join(certs, "ca.pem"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	down := "tcp://" + l.Addr().String()
	l.Close()
	t.Setenv("BUILDKIT_CERT_PATH", certs)
	t.Setenv("BUILDKIT_HOST", down)

	if bk := probeBuildkit(); bk != nil {
		t.Errorf("probeBuildkit() = %+v, want nil for a refusing endpoint", *bk)
	}
}

func TestProbeBuildkit_NoCerts(t *testing.T) {
	t.Setenv("BUILDKIT_CERT_PATH", t.TempDir())
	t.Setenv("BUILDKIT_HOST", backendListen(t))
	if bk := probeBuildkit(); bk != nil {
		t.Errorf("probeBuildkit() = %+v, want nil without the client CA", *bk)
	}
}

// TestProbeDinD_UsesDockerHost pins the same for a remote Docker daemon: it is
// probed at the endpoint DOCKER_HOST names, not a hardcoded dind:2376.
func TestProbeDinD_UsesDockerHost(t *testing.T) {
	endpoint := backendListen(t)
	t.Setenv("DOCKER_HOST", endpoint)
	t.Setenv("DOCKER_CERT_PATH", "")

	d := probeDinD()
	if d == nil {
		t.Fatalf("probeDinD() = nil, want a backend at %s", endpoint)
	}
	if d.Kind != "dind" || d.Endpoint != endpoint || !d.Healthy {
		t.Errorf("probeDinD() = %+v", *d)
	}
}
