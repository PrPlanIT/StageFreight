package runner

import (
	"errors"
	"io/fs"
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

// buildKitCertFiles is the client PKI a remote buildkitd needs.
var buildKitCertFiles = []string{"ca.pem", "cert.pem", "key.pem"}

// BuildKitCertsPresent reports whether the full client PKI in dir is readable. TLS is
// mandatory for a remote buildkitd, so a partial or unreadable set counts as absent.
func BuildKitCertsPresent(dir string) bool {
	for _, f := range buildKitCertFiles {
		fh, err := os.Open(filepath.Join(dir, f))
		if err != nil {
			return false
		}
		fh.Close()
	}
	return true
}

// unreadableBuildKitCerts lists the client PKI files in dir that exist but this uid
// can't read.
func unreadableBuildKitCerts(dir string) []string {
	var out []string
	for _, f := range buildKitCertFiles {
		path := filepath.Join(dir, f)
		if fh, err := os.Open(path); err == nil {
			fh.Close()
		} else if errors.Is(err, fs.ErrPermission) {
			out = append(out, path)
		}
	}
	return out
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
