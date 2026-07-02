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
	// kustomize renders Flux build roots offline for the gitops audition gate.
	// Release tags are prefixed "kustomize/" — %2F is the URL-encoded slash.
	"kustomize": {
		Name:        "kustomize",
		BinaryName:  "kustomize",
		DefaultVer:  "5.5.0",
		GitHubOwner: "kubernetes-sigs",
		GitHubRepo:  "kustomize",
		DownloadURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%%2Fv%s/kustomize_v%s_linux_amd64.tar.gz", ver, ver)
		},
		ChecksumURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%%2Fv%s/checksums.txt", ver)
		},
		Format: "tar.gz",
	},
	// kubeconform schema-validates rendered manifests offline (built-ins + the
	// datree CRD catalog) for the gitops audition gate.
	"kubeconform": {
		Name:        "kubeconform",
		BinaryName:  "kubeconform",
		DefaultVer:  "0.6.7",
		GitHubOwner: "yannh",
		GitHubRepo:  "kubeconform",
		DownloadURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/yannh/kubeconform/releases/download/v%s/kubeconform-linux-amd64.tar.gz", ver)
		},
		ChecksumURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/yannh/kubeconform/releases/download/v%s/CHECKSUMS", ver)
		},
		Format: "tar.gz",
	},
	// Rust coverage/runner subcommands (musl static → fit the SF substrate).
	// cargo-llvm-cov publishes NO checksum upstream → resolves via TOFU: the fingerprint
	// is established on first use, cached, and re-verified every run. cargo-nextest
	// publishes a .sha256 sidecar → verified against upstream like any tool. Either can
	// be upgraded to a hard pin by an explicit sha in toolchains.desired (optional).
	"cargo-llvm-cov": {
		Name:        "cargo-llvm-cov",
		BinaryName:  "cargo-llvm-cov",
		DefaultVer:  "0.8.7",
		GitHubOwner: "taiki-e",
		GitHubRepo:  "cargo-llvm-cov",
		DownloadURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/taiki-e/cargo-llvm-cov/releases/download/v%s/cargo-llvm-cov-x86_64-unknown-linux-musl.tar.gz", ver)
		},
		Format: "tar.gz",
	},
	"cargo-nextest": {
		Name:        "cargo-nextest",
		BinaryName:  "cargo-nextest",
		DefaultVer:  "0.9.138",
		GitHubOwner: "nextest-rs",
		GitHubRepo:  "nextest",
		DownloadURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/nextest-rs/nextest/releases/download/cargo-nextest-%s/cargo-nextest-%s-x86_64-unknown-linux-musl.tar.gz", ver, ver)
		},
		ChecksumURL: func(ver, goos, goarch string) string {
			return fmt.Sprintf("https://github.com/nextest-rs/nextest/releases/download/cargo-nextest-%s/cargo-nextest-%s-x86_64-unknown-linux-musl.sha256", ver, ver)
		},
		Format: "tar.gz",
	},
}

// LookupTool returns the ToolDef for a named tool.
func LookupTool(name string) (ToolDef, bool) {
	def, ok := registry[name]
	return def, ok
}
