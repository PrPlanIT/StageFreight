package docker

import (
	"fmt"
	"net"
	"os"
	"time"
)

// Backend represents a resolved build execution backend.
type Backend struct {
	Kind     string // "buildkitd" or "dind"
	Endpoint string // e.g., "tcp://buildkitd:1234" or "tcp://dind:2376"
	CertPath string // TLS cert directory
	Healthy  bool
}

// BackendCapabilities describes what an operation requires.
type BackendCapabilities struct {
	Build      bool // needs image build
	Run        bool // needs container execution (docker run)
	Push       bool // needs registry push
	Filesystem bool // needs shared filesystem with job container
}

// ResolveBackend selects the best backend for the given operation.
// Default: prefer buildkitd (persistent cache) → fall back to DinD.
// Config override: "buildkitd" forces buildkitd (fail if unavailable),
// "dind" forces DinD (ignore buildkitd even if available).
//
// Returns the resolved backend. Callers should not mix backends within
// a single operation — use the returned backend for everything.
func ResolveBackend(caps BackendCapabilities) (*Backend, error) {
	return ResolveBackendWithConfig(caps, "")
}

// ResolveBackendWithConfig selects backend with explicit config override.
func ResolveBackendWithConfig(caps BackendCapabilities, configBackend string) (*Backend, error) {
	bk := probeBuildkit()
	dind := probeDinD()

	switch configBackend {
	case "buildkitd":
		// Explicit: must use buildkitd.
		if bk == nil || !bk.Healthy {
			return nil, fmt.Errorf("config requires buildkitd backend but it is not available")
		}
		return bk, nil

	case "dind":
		// Explicit: must use DinD, ignore buildkitd.
		if dind == nil || !dind.Healthy {
			return nil, fmt.Errorf("config requires dind backend but it is not available")
		}
		return dind, nil
	}

	// Auto-detect: prefer buildkitd, fall back to DinD.
	if bk != nil && bk.Healthy {
		return bk, nil
	}
	if dind != nil && dind.Healthy {
		return dind, nil
	}

	return nil, fmt.Errorf("no healthy build backend available (buildkitd and DinD both unreachable)")
}

// probeBuildkit checks if a persistent buildkitd is available.
func probeBuildkit() *Backend {
	bkCertPath := os.Getenv("BUILDKIT_CERT_PATH")
	if bkCertPath == "" {
		bkCertPath = "/buildkit-certs"
	}

	// Cert presence is the discovery signal.
	if _, err := os.Stat(bkCertPath + "/ca.pem"); err != nil {
		return nil
	}

	endpoint := os.Getenv("BUILDKIT_HOST")
	if endpoint == "" {
		endpoint = "tcp://buildkitd:1234"
	}

	// Verify reachable (TCP probe, not full TLS — EnsureBuilder does the real check).
	if !tcpReachable("buildkitd", 1234) {
		return nil
	}

	return &Backend{
		Kind:     "buildkitd",
		Endpoint: endpoint,
		CertPath: bkCertPath,
		Healthy:  true,
	}
}

// probeDinD checks if DinD is available.
func probeDinD() *Backend {
	dockerHost := os.Getenv("DOCKER_HOST")
	if dockerHost == "" {
		// Check local socket.
		if _, err := os.Stat("/var/run/docker.sock"); err != nil {
			return nil
		}
		return &Backend{
			Kind:     "dind",
			Endpoint: "unix:///var/run/docker.sock",
			Healthy:  true,
		}
	}

	certPath := os.Getenv("DOCKER_CERT_PATH")

	// TCP probe for remote Docker.
	if !tcpReachable("dind", 2376) {
		return nil
	}

	return &Backend{
		Kind:     "dind",
		Endpoint: dockerHost,
		CertPath: certPath,
		Healthy:  true,
	}
}

// tcpReachable checks if a host:port is reachable within 2 seconds.
func tcpReachable(host string, port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// IsBuildkit returns true if this backend is a persistent buildkitd.
func (b *Backend) IsBuildkit() bool {
	return b.Kind == "buildkitd"
}
