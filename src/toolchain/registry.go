package toolchain

import "fmt"

// ToolDef describes how to download and verify a tool.
type ToolDef struct {
	Name         string
	BinaryName   string                                    // name of the binary file
	DefaultVer   string                                    // default version if none specified
	DownloadURL  func(version, goos, goarch string) string // archive or binary URL
	ChecksumURL  func(version, goos, goarch string) string // checksum file URL
	Format       string                                    // "tar.gz" or "binary"
	ArchiveStrip string                                    // prefix to strip from tar entries (e.g. "go/")
	GitHubOwner  string                                    // GitHub owner for release checking (e.g. "aquasecurity")
	GitHubRepo   string                                    // GitHub repo for release checking (e.g. "trivy")
}

// AllTools returns a copy of all registered tool definitions.
// Callers can inspect metadata without mutating the registry.
func AllTools() []ToolDef {
	defs := make([]ToolDef, 0, len(registry))
	for _, def := range registry {
		defs = append(defs, def)
	}
	return defs
}

var registry = map[string]ToolDef{
	"trivy": {
		Name:        "trivy",
		BinaryName:  "trivy",
		DefaultVer:  "0.69.3",
		GitHubOwner: "aquasecurity",
		GitHubRepo:  "trivy",
		DownloadURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/aquasecurity/trivy/releases/download/v%s/trivy_%s_Linux-64bit.tar.gz", ver, ver)
		},
		ChecksumURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/aquasecurity/trivy/releases/download/v%s/trivy_%s_checksums.txt", ver, ver)
		},
		Format: "tar.gz",
	},
	"syft": {
		Name:        "syft",
		BinaryName:  "syft",
		DefaultVer:  "1.42.3",
		GitHubOwner: "anchore",
		GitHubRepo:  "syft",
		DownloadURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/anchore/syft/releases/download/v%s/syft_%s_linux_amd64.tar.gz", ver, ver)
		},
		ChecksumURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/anchore/syft/releases/download/v%s/syft_%s_checksums.txt", ver, ver)
		},
		Format: "tar.gz",
	},
	"grype": {
		Name:        "grype",
		BinaryName:  "grype",
		DefaultVer:  "0.110.0",
		GitHubOwner: "anchore",
		GitHubRepo:  "grype",
		DownloadURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/anchore/grype/releases/download/v%s/grype_%s_linux_amd64.tar.gz", ver, ver)
		},
		ChecksumURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/anchore/grype/releases/download/v%s/grype_%s_checksums.txt", ver, ver)
		},
		Format: "tar.gz",
	},
	"osv-scanner": {
		Name:        "osv-scanner",
		BinaryName:  "osv-scanner",
		DefaultVer:  "2.3.5",
		GitHubOwner: "google",
		GitHubRepo:  "osv-scanner",
		DownloadURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/google/osv-scanner/releases/download/v%s/osv-scanner_linux_amd64", ver)
		},
		ChecksumURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/google/osv-scanner/releases/download/v%s/osv-scanner_SHA256SUMS", ver)
		},
		Format: "binary",
	},
	"cosign": {
		Name:        "cosign",
		BinaryName:  "cosign",
		DefaultVer:  "3.0.6",
		GitHubOwner: "sigstore",
		GitHubRepo:  "cosign",
		DownloadURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/sigstore/cosign/releases/download/v%s/cosign-linux-amd64", ver)
		},
		ChecksumURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/sigstore/cosign/releases/download/v%s/cosign_checksums.txt", ver)
		},
		Format: "binary",
	},
	"flux": {
		Name:        "flux",
		BinaryName:  "flux",
		DefaultVer:  "2.8.3",
		GitHubOwner: "fluxcd",
		GitHubRepo:  "flux2",
		DownloadURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/fluxcd/flux2/releases/download/v%s/flux_%s_linux_amd64.tar.gz", ver, ver)
		},
		ChecksumURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/fluxcd/flux2/releases/download/v%s/flux_%s_checksums.txt", ver, ver)
		},
		Format: "tar.gz",
	},
	"kubectl": {
		Name:       "kubectl",
		BinaryName: "kubectl",
		DefaultVer: "1.34.2",
		DownloadURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://dl.k8s.io/release/v%s/bin/linux/amd64/kubectl", ver)
		},
		ChecksumURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://dl.k8s.io/release/v%s/bin/linux/amd64/kubectl.sha256", ver)
		},
		Format: "binary",
	},
}

// LookupTool returns the ToolDef for a named tool.
func LookupTool(name string) (ToolDef, bool) {
	def, ok := registry[name]
	return def, ok
}
