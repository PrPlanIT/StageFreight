package toolchain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ToolSource materializes a tool's binary into a versioned install directory. It is the seam
// that lets StageFreight provision tools that arrive by fundamentally different means — a
// released binary download, `go install`, and later `cargo install` / npm / pip — without any
// of them being a special case in the resolver.
//
// The division of labour is strict: the resolver owns caching, install locking, the binary
// checksum, and metadata; a source only knows how to OBTAIN the binary and report its
// provenance. Because the cache lookup lives above the source, every source inherits the same
// version-keyed persistent cache — a source runs only on a cache miss.
type ToolSource interface {
	// Materialize places the tool binary at req.BinPath and returns its provenance. bin/ under
	// req.InstallDir already exists. Errors are returned bare; the resolver adds the
	// "toolchain <name> <version>:" prefix.
	Materialize(req SourceRequest) (SourceResult, error)
}

// SourceRequest is everything a source needs, filled by the resolver from the ToolDef, the
// resolved version, the platform, and the chosen versioned install directory.
type SourceRequest struct {
	Def        ToolDef
	Version    string
	GOOS       string
	GOARCH     string
	RootDir    string // workspace root — for sources that resolve a sibling toolchain (go install needs go)
	InstallDir string // the versioned dir; bin/ already created under it
	BinPath    string // where the binary must end up (== <InstallDir>/bin/<binary>)
	PinnedSHA  string // explicit config fingerprint, or ""
}

// SourceResult is the provenance a source reports. The resolver folds it into Metadata
// alongside the binary checksum it computes itself.
type SourceResult struct {
	SourceURL string // where/what it materialized from (a URL, or a `go install module@ver` ref)
	SHA256    string // artifact digest when there is an archive; "" when there is none (e.g. go install)
	Trust     string // TrustPinned | TrustChecksum | TrustTOFU
}

// releaseBinarySource is the default source: download an official release artifact, verify its
// checksum (pinned → upstream ChecksumURL → TOFU), and install per ToolDef.Format. This is the
// behaviour every download-based tool (trivy, osv-scanner, …) has always had — lifted verbatim
// out of the resolver so it becomes one source among several rather than the only path.
type releaseBinarySource struct{}

func (releaseBinarySource) Materialize(req SourceRequest) (SourceResult, error) {
	def := req.Def
	sourceURL := def.DownloadURL(req.Version, req.GOOS, req.GOARCH)
	downloadFilename := filepath.Base(sourceURL)

	// Establish the trust source, strongest available first: an explicit config pin, then the
	// upstream-published checksum, then TOFU — first-use trust when upstream offers no claim.
	var expectedSHA, trust string
	switch {
	case req.PinnedSHA != "":
		expectedSHA, trust = req.PinnedSHA, TrustPinned
	case def.ChecksumURL != nil:
		var err error
		expectedSHA, err = fetchChecksumFromURL(def.ChecksumURL(req.Version, req.GOOS, req.GOARCH), downloadFilename)
		if err != nil {
			return SourceResult{}, err
		}
		trust = TrustChecksum
	default:
		trust = TrustTOFU
	}

	archivePath, err := downloadToTemp(sourceURL)
	if err != nil {
		return SourceResult{}, fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(archivePath)

	actualSHA, err := fileSHA256(archivePath)
	if err != nil {
		return SourceResult{}, fmt.Errorf("checksum computation failed: %w", err)
	}
	if expectedSHA != "" && actualSHA != expectedSHA {
		return SourceResult{}, fmt.Errorf("checksum mismatch\n  expected: %s\n  actual:   %s\n  source:   %s", expectedSHA, actualSHA, sourceURL)
	}
	// TOFU (expectedSHA == ""): the computed digest becomes the established fingerprint,
	// persisted by the resolver and re-verified against the cached binary on every later run.

	switch def.Format {
	case "binary":
		if err := installStandaloneBinary(archivePath, req.BinPath); err != nil {
			return SourceResult{}, fmt.Errorf("install failed: %w", err)
		}
	case "tar.gz":
		if err := installFromArchive(archivePath, req.BinPath, def.BinaryName); err != nil {
			return SourceResult{}, fmt.Errorf("install failed: %w", err)
		}
	default:
		return SourceResult{}, fmt.Errorf("unsupported format %q", def.Format)
	}
	return SourceResult{SourceURL: sourceURL, SHA256: actualSHA, Trust: trust}, nil
}

// GoInstallSource provisions a Go module tool via `go install module@version`. This is the
// provisioning model for the whole class of go-installable tools (govulncheck, stringer,
// mockgen, controller-gen, …), none of which ship release binaries. The Go toolchain that
// builds the tool is itself resolved through the toolchain — no host fallback — defaulting to
// the repository's own Go version so nothing hardcodes one. Module integrity is enforced by the
// Go checksum database during install; the resulting binary is trusted TOFU (its digest is
// established on first use and re-verified every run, exactly like any tool without an upstream
// binary checksum).
type GoInstallSource struct {
	Module    string // e.g. "golang.org/x/vuln/cmd/govulncheck"
	GoVersion string // Go toolchain used to BUILD the tool; "" → the repository's go directive

	// Injection seams for tests; nil selects the real implementations.
	run       func(goBin string, args, env []string) error
	resolveGo func(rootDir, version string) (Result, error)
}

func (s GoInstallSource) Materialize(req SourceRequest) (SourceResult, error) {
	if s.Module == "" {
		return SourceResult{}, fmt.Errorf("GoInstallSource: empty module")
	}
	// Resolve the Go toolchain that will BUILD the tool. A standalone tool binary is
	// independent of the analyzed repo's build, so a source-pinned GoVersion — or, by default,
	// the repository's own go directive — is used.
	goVer := s.GoVersion
	if goVer == "" {
		goVer = ResolveGoVersion(req.RootDir, req.RootDir)
	}
	resolveGo := s.resolveGo
	if resolveGo == nil {
		resolveGo = func(rootDir, version string) (Result, error) { return Resolve(rootDir, "go", version) }
	}
	goRes, err := resolveGo(req.RootDir, goVer)
	if err != nil {
		return SourceResult{}, fmt.Errorf("resolving Go to build %s: %w", s.Module, err)
	}

	ref := goInstallRef(s.Module, req.Version)
	binDir := filepath.Dir(req.BinPath)

	// GOBIN lands the built binary in our versioned bin dir; the persistent module + build
	// caches keep even the first-per-version install warm.
	env := append(os.Environ(), "GOBIN="+binDir, "GOFLAGS=-buildvcs=false")
	if gomod, gocache := GoCacheDirs(); gomod != "" {
		env = append(env, "GOMODCACHE="+gomod, "GOCACHE="+gocache)
	}

	run := s.run
	if run == nil {
		run = execGoInstall
	}
	if err := run(goRes.Path, []string{"install", ref}, env); err != nil {
		return SourceResult{}, fmt.Errorf("go install %s: %w", ref, err)
	}
	return SourceResult{SourceURL: "go install " + ref, Trust: TrustTOFU}, nil
}

// goInstallRef builds the `module@version` argument. Go module versions are v-prefixed
// semver; the registry stores the bare version (like every other tool) and we add the "v".
func goInstallRef(module, version string) string {
	if version == "" {
		return module + "@latest"
	}
	if version[0] != 'v' {
		version = "v" + version
	}
	return module + "@" + version
}

func execGoInstall(goBin string, args, env []string) error {
	cmd := exec.Command(goBin, args...)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}
