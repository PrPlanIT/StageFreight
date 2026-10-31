package runner

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BuildKitCertPath is the directory holding the BuildKit client PKI (ca.pem,
// cert.pem, key.pem): BUILDKIT_CERT_PATH, default /buildkit-certs. The build
// path, prune and preflight all read the same directory.
func BuildKitCertPath() string {
	if p := os.Getenv("BUILDKIT_CERT_PATH"); p != "" {
		return p
	}
	return "/buildkit-certs"
}

// BuildKitCertsPresent reports whether the full client PKI is in dir. TLS is
// mandatory for a remote buildkitd, so a partial set counts as absent.
func BuildKitCertsPresent(dir string) bool {
	for _, f := range []string{"ca.pem", "cert.pem", "key.pem"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			return false
		}
	}
	return true
}

// TCPEndpointAddr returns host:port from a tcp:// endpoint such as
// "tcp://buildkitd.ci.svc:1234". Anything else (unix://, bare host) is not a
// TCP endpoint.
func TCPEndpointAddr(endpoint string) (string, bool) {
	rest, ok := strings.CutPrefix(endpoint, "tcp://")
	if !ok {
		return "", false
	}
	rest = strings.TrimSuffix(rest, "/")
	if _, _, err := net.SplitHostPort(rest); err != nil {
		return "", false
	}
	return rest, true
}

// TCPReachable reports whether addr accepts a connection within two seconds.
func TCPReachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// RemoteBuildKit returns BUILDKIT_HOST when a remote buildkitd is usable the way
// the build path uses it (buildx remote driver): a tcp:// endpoint, the full
// client PKI present, and the endpoint accepting connections. "" otherwise.
// It needs no Docker daemon and no buildctl binary.
func RemoteBuildKit() string {
	endpoint := strings.TrimSpace(os.Getenv("BUILDKIT_HOST"))
	addr, ok := TCPEndpointAddr(endpoint)
	if !ok || !BuildKitCertsPresent(BuildKitCertPath()) || !TCPReachable(addr) {
		return ""
	}
	return endpoint
}
