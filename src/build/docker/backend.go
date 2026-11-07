package docker

import (
	"fmt"
	"os"

	"github.com/PrPlanIT/StageFreight/src/runner"
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
	// canSatisfy guards against selecting a backend that can't fulfill the operation.
	if bk != nil && bk.Healthy && canSatisfy(bk, caps) {
		return bk, nil
	}
	if dind != nil && dind.Healthy && canSatisfy(dind, caps) {
		return dind, nil
	}

	return nil, fmt.Errorf("no healthy build backend available (buildkitd and DinD both unreachable)")
}

// probeBuildkit checks if a persistent buildkitd is available.
func probeBuildkit() *Backend {
	bkCertPath := runner.BuildKitCertPath()

	// Cert presence is the discovery signal.
	if _, err := os.Stat(bkCertPath + "/ca.pem"); err != nil {
		return nil
	}

	endpoint := runner.BuildKitEndpoint()

	// Verify reachable at that endpoint (TCP probe, not full
	// TLS — EnsureBuilder does the real check).
	addr, ok := runner.TCPEndpointAddr(endpoint)
	if !ok || !runner.TCPReachable(addr) {
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

	// TCP probe for remote Docker, at the endpoint DOCKER_HOST names.
	addr, ok := runner.TCPEndpointAddr(dockerHost)
	if !ok || !runner.TCPReachable(addr) {
		return nil
	}

	return &Backend{
		Kind:     "dind",
		Endpoint: dockerHost,
		CertPath: certPath,
		Healthy:  true,
	}
}

// IsBuildkit returns true if this backend is a persistent buildkitd.
func (b *Backend) IsBuildkit() bool {
	return b.Kind == "buildkitd"
}

// canSatisfy checks if a backend can fulfill the required capabilities.
// Currently all known backends satisfy all known operations — this guard
// exists to prevent future regressions when new capability requirements
// are added that not all backends support.
func canSatisfy(b *Backend, caps BackendCapabilities) bool {
	// Both buildkitd and DinD satisfy all current operations.
	// BuildKit: build + push natively, run via binary extraction.
	// DinD: build + push + run via docker daemon.
	// Guard will become meaningful when new operations are added
	// (e.g., multi-arch cross-build, attestation signing).
	return b.Healthy
}
