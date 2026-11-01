package runner

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// writeCerts creates the files named in dir, standing in for the BuildKit client PKI.
func writeCerts(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// listen opens a TCP listener on localhost and returns its tcp:// endpoint.
func listen(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return "tcp://" + l.Addr().String()
}

func TestTCPEndpointAddr(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"tcp://buildkitd:1234", "buildkitd:1234", true},
		{"tcp://buildkitd.optcp-ci-runners.svc:1234", "buildkitd.optcp-ci-runners.svc:1234", true},
		{"tcp://10.0.0.5:1234/", "10.0.0.5:1234", true},
		{"tcp://buildkitd", "", false}, // no port
		{"unix:///run/buildkit/buildkitd.sock", "", false},
		{"buildkitd:1234", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := TCPEndpointAddr(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("TCPEndpointAddr(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestRemoteBuildKit pins the daemonless discovery: a remote buildkitd counts
// only with a tcp:// BUILDKIT_HOST, the full client PKI, and a listening
// endpoint, and it is probed at the endpoint BUILDKIT_HOST names (never a
// hardcoded host).
func TestRemoteBuildKit(t *testing.T) {
	full := t.TempDir()
	writeCerts(t, full, "ca.pem", "cert.pem", "key.pem")
	partial := t.TempDir()
	writeCerts(t, partial, "ca.pem")
	up := listen(t)

	// An endpoint that refuses: a listener opened and closed again.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	down := "tcp://" + l.Addr().String()
	l.Close()

	cases := []struct {
		name, host, certs, want string
	}{
		{"reachable with full PKI", up, full, up},
		{"unset BUILDKIT_HOST", "", full, ""},
		{"unix socket is not remote", "unix:///run/buildkit/buildkitd.sock", full, ""},
		{"partial PKI", up, partial, ""},
		{"no PKI", up, t.TempDir(), ""},
		{"unreachable endpoint", down, full, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("BUILDKIT_HOST", c.host)
			t.Setenv("BUILDKIT_CERT_PATH", c.certs)
			if got := RemoteBuildKit(); got != c.want {
				t.Errorf("RemoteBuildKit() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBuildKitCertPath_Default(t *testing.T) {
	t.Setenv("BUILDKIT_CERT_PATH", "")
	if got := BuildKitCertPath(); got != "/buildkit-certs" {
		t.Errorf("default cert path = %q, want /buildkit-certs", got)
	}
}

// TestDockerDaemon pins the endpoint DockerDaemon reports on each kind of host:
// a tcp DOCKER_HOST is reported even when nothing answers, a unix socket only
// when present, and no daemon is claimed where none answers.
func TestDockerDaemon(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "docker.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, host, wantEndpoint string
	}{
		{"tcp endpoint, nothing answering", "tcp://127.0.0.1:1", "tcp://127.0.0.1:1"},
		{"unix socket present, nothing answering", "unix://" + sock, sock},
		{"unix socket absent", "unix://" + filepath.Join(t.TempDir(), "absent.sock"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DOCKER_HOST", c.host)
			endpoint, ok := DockerDaemon()
			if endpoint != c.wantEndpoint || ok {
				t.Errorf("DockerDaemon() = %q, %v; want %q, false", endpoint, ok, c.wantEndpoint)
			}
		})
	}
}
