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

	// ReleaseSource names WHERE version discovery reads the release list — declared
	// data, so the resolver dispatches on strategy, never on the tool's name: "github"
	// (releases API, the default when GitHubOwner/Repo are set) or "k8s" (dl.k8s.io
	// stable channels). This is distinct from Source, which materializes the binary.
	ReleaseSource string

	// Source materializes the binary. When nil, the tool is a released-binary download
	// (DownloadURL + Format) — the historical default. A non-nil Source (e.g. GoInstallSource)
	// provisions by another means; DownloadURL/ChecksumURL/Format are then unused.
	Source ToolSource
}

// source returns the tool's materialization source: its explicit Source, or the default
// released-binary downloader. Keeps the resolver source-agnostic — download tools and
// go-install tools flow through one code path.
func (d ToolDef) source() ToolSource {
	if d.Source != nil {
		return d.Source
	}
	return releaseBinarySource{}
}

// ReleaseSourceKind resolves the version-discovery strategy: an explicit
// ReleaseSource, else "github" when a GitHub repo is declared, else "" (no upstream
// version discovery). Lets the resolver dispatch on capability, not identity.
func (d ToolDef) ReleaseSourceKind() string {
	if d.ReleaseSource != "" {
		return d.ReleaseSource
	}
	if d.GitHubOwner != "" && d.GitHubRepo != "" {
		return "github"
	}
	return ""
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
	// govulncheck ships no release binaries — it is `go install`-only — so it is provisioned
	// through GoInstallSource rather than a DownloadURL. It backs the Go reachability evidence
	// contributor (call-graph analysis over the built module).
	"govulncheck": {
		Name:       "govulncheck",
		BinaryName: "govulncheck",
		DefaultVer: "1.5.0",
		GitHubOwner: "golang",
		GitHubRepo:  "vuln",
		Source:     GoInstallSource{Module: "golang.org/x/vuln/cmd/govulncheck"},
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
		Name:          "kubectl",
		BinaryName:    "kubectl",
		DefaultVer:    "1.34.2",
		ReleaseSource: "k8s", // dl.k8s.io stable channels, not GitHub releases
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
