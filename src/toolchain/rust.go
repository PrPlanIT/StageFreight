package toolchain

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/substrate"
)

// Rust mirrors the Go toolchain model: StageFreight owns the toolchain — official
// distribution artifact, verified checksum, explicit install layout, no rustup as
// the authoritative state, no ambient `rust:` container. The combined standalone
// tarball (rustc + cargo + std) is installed to a pinned prefix via its bundled
// install.sh; cargo at <prefix>/bin/cargo is the resolved binary.

// rustHostTriple projects the host GOOS/GOARCH (+ libc on Linux) to the Rust host
// target triple for the TOOLCHAIN download. The libc matters: Rust's prebuilt
// toolchains are libc-specific — a -gnu cargo cannot exec on a musl host (its ELF
// interpreter is absent) and vice-versa — so a musl host (Alpine) needs the
// -unknown-linux-musl toolchain. (Cross-compilation target-triple projection is the
// engine adapter's concern, later.)
func rustHostTriple(goos, goarch, libc string) string {
	arch := map[string]string{
		"amd64": "x86_64", "arm64": "aarch64", "arm": "armv7", "386": "i686",
	}[goarch]
	if arch == "" {
		arch = goarch
	}
	switch goos {
	case "linux":
		if libc == "" {
			libc = "gnu"
		}
		return arch + "-unknown-linux-" + libc
	case "darwin":
		return arch + "-apple-darwin"
	case "windows":
		return arch + "-pc-windows-msvc"
	default:
		return arch + "-unknown-" + goos
	}
}

// hostLibc reports the host C library on Linux ("musl" or "gnu"), "" elsewhere.
// Selects the Rust toolchain libc so the downloaded cargo can actually exec on the
// host — the root cause of "fork/exec cargo: no such file or directory" (a -gnu
// binary whose glibc interpreter is missing) when running on Alpine/musl.
func hostLibc() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if arch == "" {
		arch = runtime.GOARCH
	}
	if _, err := os.Stat("/lib/ld-musl-" + arch + ".so.1"); err == nil {
		return "musl"
	}
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		return "musl"
	}
	return "gnu"
}

func rustDownloadURL(version, triple string) string {
	return fmt.Sprintf("https://static.rust-lang.org/dist/rust-%s-%s.tar.gz", version, triple)
}

// ResolveRustVersion reads the pinned toolchain from rust-toolchain.toml /
// rust-toolchain (the Rust equivalent of go.mod's toolchain directive). It honors an
// exact numeric pin ("1.86.0") AND a named channel ("stable"/"beta"/"nightly") — a
// named channel is resolved to its CONCRETE current version at download time (so a run
// is reproducible and cached, while a project that tracks stable still builds). The
// default is "stable", matching rustup, rather than a stale numeric default that would
// fail to compile a newer edition.
func ResolveRustVersion(dir, repoRoot string) string {
	for _, p := range []string{
		filepath.Join(dir, "rust-toolchain.toml"),
		filepath.Join(dir, "rust-toolchain"),
		filepath.Join(repoRoot, "rust-toolchain.toml"),
		filepath.Join(repoRoot, "rust-toolchain"),
	} {
		if v := parseRustChannel(p); v != "" {
			return v
		}
	}
	return defaultRustChannel
}

const defaultRustChannel = "stable"

func isNamedChannel(s string) bool {
	return s == "stable" || s == "beta" || s == "nightly"
}

// parseRustChannel extracts the pinned toolchain from a rust-toolchain[.toml] file —
// an exact numeric version or a named channel. Handles `channel = "stable"` (toml) and
// a bare `1.86.0` / `stable` (legacy plain file).
func parseRustChannel(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "channel") {
			if i := strings.IndexByte(line, '='); i >= 0 {
				line = strings.TrimSpace(line[i+1:])
			}
		}
		v := strings.Trim(line, `"'`)
		if isNumericVersion(v) || isNamedChannel(v) {
			return v
		}
	}
	return ""
}

// resolveRustChannelVersion resolves a named channel (stable/beta/nightly) to its
// concrete version via Rust's official channel manifest, so the download URL and cache
// key are a pinned version even though the project tracks a channel.
func resolveRustChannelVersion(channel string) (string, error) {
	url := "https://static.rust-lang.org/dist/channel-rust-" + channel + ".toml"
	resp, err := httpGet(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("channel manifest %s: HTTP %d", channel, resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	inRust := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inRust = line == "[pkg.rust]"
			continue
		}
		if inRust && strings.HasPrefix(line, "version") {
			// version = "1.86.0 (<hash> <date>)"
			if i := strings.IndexByte(line, '='); i >= 0 {
				val := strings.Trim(strings.TrimSpace(line[i+1:]), `"'`)
				if v := strings.Fields(val); len(v) > 0 && isNumericVersion(v[0]) {
					return v[0], nil
				}
			}
		}
	}
	return "", fmt.Errorf("channel manifest %s: no [pkg.rust] version found", channel)
}

func isNumericVersion(s string) bool {
	if s == "" || (s[0] < '0' || s[0] > '9') {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

// resolveRust ensures a verified Rust toolchain is installed and returns cargo's path.
// Special-cased like Go (full distribution + bundled installer, not a single binary).
func resolveRust(rootDir, version string) (Result, error) {
	// A named channel (stable/beta/nightly) resolves to its concrete current version
	// before anything is downloaded or cached, so the install is a pinned version.
	if isNamedChannel(version) {
		concrete, err := resolveRustChannelVersion(version)
		if err != nil {
			return Result{}, fmt.Errorf("toolchain rust: resolving %q channel: %w", version, err)
		}
		version = concrete
	}
	libc := hostLibc()
	triple := rustHostTriple(runtime.GOOS, runtime.GOARCH, libc)
	sourceURL := rustDownloadURL(version, triple)

	// Cache key is libc-qualified: a -gnu and a -musl build of the same Rust version
	// are different toolchains and must never share a cache slot (a stale -gnu cargo
	// cannot exec on a musl host). Result.Version stays the plain Rust version.
	cacheKey := version
	if libc != "" {
		cacheKey = version + "-" + libc
	}

	// Cache search across read roots.
	for _, root := range ReadRoots(rootDir) {
		binPath := CacheBinPathIn(root, "rust", cacheKey, "cargo")
		if _, err := os.Stat(binPath); err != nil {
			continue
		}
		meta, metaErr := readMetadataFrom(root, "rust", cacheKey)
		if metaErr != nil || meta.BinSHA256 == "" {
			continue
		}
		if actual, hashErr := fileSHA256(binPath); hashErr == nil && actual == meta.BinSHA256 {
			return Result{Tool: "rust", Version: version, Path: binPath, CacheHit: true,
				SourceURL: meta.SourceURL, SHA256: meta.SHA256, BinSHA256: meta.BinSHA256}, nil
		}
	}

	installRoot := InstallRoot(rootDir)
	installDir := CacheDirIn(installRoot, "rust", cacheKey)
	lock, err := AcquireInstallLock(installDir, 15*time.Minute) // a Rust dist is large
	if err != nil {
		return Result{}, fmt.Errorf("toolchain rust %s: %w", version, err)
	}
	defer ReleaseInstallLock(lock)

	binPath := CacheBinPathIn(installRoot, "rust", cacheKey, "cargo")
	if _, err := os.Stat(binPath); err == nil {
		if meta, metaErr := readMetadataFrom(installRoot, "rust", cacheKey); metaErr == nil && meta.BinSHA256 != "" {
			if actual, hashErr := fileSHA256(binPath); hashErr == nil && actual == meta.BinSHA256 {
				return Result{Tool: "rust", Version: version, Path: binPath, CacheHit: true,
					SourceURL: meta.SourceURL, SHA256: meta.SHA256, BinSHA256: meta.BinSHA256}, nil
			}
		}
	}

	expectedSHA, err := fetchChecksumFromURL(sourceURL+".sha256", filepath.Base(sourceURL))
	if err != nil {
		return Result{}, fmt.Errorf("toolchain rust %s: fetching checksum: %w", version, err)
	}
	archivePath, err := downloadToTemp(sourceURL)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain rust %s: download failed: %w", version, err)
	}
	defer os.Remove(archivePath)
	archiveSHA, err := fileSHA256(archivePath)
	if err != nil {
		return Result{}, fmt.Errorf("toolchain rust %s: checksum computation failed: %w", version, err)
	}
	if archiveSHA != expectedSHA {
		return Result{}, fmt.Errorf("toolchain rust %s: archive checksum mismatch\n  expected: %s\n  actual:   %s\n  source:   %s", version, expectedSHA, archiveSHA, sourceURL)
	}

	if err := installRustDist(archivePath, installDir, version, triple); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain rust %s: install failed: %w", version, err)
	}
	if _, err := os.Stat(binPath); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain rust %s: cargo not found after install at %s", version, binPath)
	}
	binSHA, err := fileSHA256(binPath)
	if err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain rust %s: binary checksum failed: %w", version, err)
	}
	meta := Metadata{Tool: "rust", Version: version, Platform: fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		SourceURL: sourceURL, SHA256: archiveSHA, BinSHA256: binSHA}
	if err := writeMetadataTo(installRoot, "rust", cacheKey, meta); err != nil {
		os.RemoveAll(installDir)
		return Result{}, fmt.Errorf("toolchain rust %s: metadata write failed (install aborted): %w", version, err)
	}
	return Result{Tool: "rust", Version: version, Path: binPath, CacheHit: false,
		SourceURL: sourceURL, SHA256: archiveSHA, BinSHA256: binSHA}, nil
}

// installRustDist extracts the combined Rust tarball and runs its bundled install.sh
// to a pinned prefix (no ldconfig, no docs) — a POSIX-sh installer that copies rustc,
// cargo, and the host std into <installDir>/{bin,lib}.
func installRustDist(archivePath, installDir, version, triple string) error {
	tmp, err := os.MkdirTemp("", "sf-rust-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := extractTarGzTo(archivePath, tmp); err != nil {
		return fmt.Errorf("extracting rust dist: %w", err)
	}

	// Rust's install.sh shells out to bash, but the minimal SF image ships only
	// busybox sh — so realize bash via substrate (apk-backed, cached; TEST/BUILD
	// time only, never in the image) before running it. Best-effort: on a non-apk
	// host (dev) NewRealizer is a no-op that trusts the ambient bash.
	_, _ = substrate.NewRealizer(SubstrateCacheDir()).Realize(context.Background(),
		[]substrate.Need{{Capability: "bash", Reason: "rust-toolchain-install-script", Source: "install.sh"}})

	script := filepath.Join(tmp, fmt.Sprintf("rust-%s-%s", version, triple), "install.sh")
	cmd := exec.Command("sh", script,
		"--prefix="+installDir, "--disable-ldconfig", "--without=rust-docs")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install.sh: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// extractTarGzTo extracts a .tar.gz tree verbatim into destDir (no prefix stripping).
func extractTarGzTo(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		// Reject path traversal.
		target := filepath.Join(destDir, hdr.Name)
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path in archive: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, tr); err != nil {
				w.Close()
				return err
			}
			w.Close()
		case tar.TypeSymlink:
			_ = os.Symlink(hdr.Linkname, target)
		}
	}
}

// CargoCacheDir returns the persistent CARGO_HOME (rust/downloads) — crate sources +
// registry index reused across runs, the Rust analog of GOMODCACHE. "" off-mount.
func CargoCacheDir() string { return cacheDir("rust", "downloads") }

// SubstrateCacheDir returns the persistent apk package cache (substrate/apk) — so
// native build-substrate realization is download-once, offline-after-first. "" off-mount.
func SubstrateCacheDir() string { return cacheDir("substrate", "apk") }

// CargoTargetDir returns a persistent, per-project CARGO_TARGET_DIR (rust/build/<key>),
// the Rust analog of GOCACHE: dependency crates (and their C build outputs, e.g.
// aws-lc) compile once and are reused — only changed local code rebuilds. Keyed PER
// PROJECT so concurrent builds don't contend on cargo's target lock. "" off-mount.
func CargoTargetDir(key string) string {
	if key == "" {
		return ""
	}
	return cacheDir("rust", "build", key)
}
