package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GoBuild wraps Go compilation commands.
type GoBuild struct {
	Verbose bool
	Stdout  io.Writer
	Stderr  io.Writer
}

// NewGoBuild creates a GoBuild runner with default output writers.
func NewGoBuild(verbose bool) *GoBuild {
	return &GoBuild{
		Verbose: verbose,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}
}

// GoBuildOpts holds the parameters for a single Go compilation.
type GoBuildOpts struct {
	Entry      string            // main package path (e.g., "cmd/planedc/main.go" or "./cmd/planedc")
	OutputPath string            // output binary path
	GOOS       string
	GOARCH     string
	Args       []string          // raw args passed before entry (e.g., ["-tags", "banner_art", "-ldflags", "..."])
	Env        map[string]string // additional env vars (e.g., CGO_ENABLED=0)
	GoBin      string            // resolved go binary (from the toolchain subsystem); "" falls back to "go" on $PATH
}

// GoBuildResult holds the output of a Go compilation.
type GoBuildResult struct {
	Path   string // absolute output path
	Size   int64
	SHA256 string
}

// Build compiles a Go binary with the given options.
func (g *GoBuild) Build(ctx context.Context, opts GoBuildOpts) (*GoBuildResult, error) {
	// Normalize entry path: if it ends with .go, use the directory
	entry := opts.Entry
	if strings.HasSuffix(entry, ".go") {
		entry = "./" + filepath.Dir(entry)
	} else if !strings.HasPrefix(entry, ".") && !strings.HasPrefix(entry, "/") {
		entry = "./" + entry
	}

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(opts.OutputPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating output directory: %w", err)
	}

	// Build go build args
	args := []string{"build"}

	// Append raw builder args (e.g., -tags, -ldflags, etc.)
	args = append(args, opts.Args...)

	args = append(args, "-o", opts.OutputPath, entry)

	// Use the toolchain-resolved go binary when provided; the runtime CI image
	// has no go on $PATH, so callers resolve it via the toolchain subsystem and
	// pass the absolute path here. Bare "go" remains the local/dev fallback.
	goBin := opts.GoBin
	if goBin == "" {
		goBin = "go"
	}
	cmd := exec.CommandContext(ctx, goBin, args...)

	// Set up environment
	cmd.Env = os.Environ()
	if opts.GOOS != "" {
		cmd.Env = append(cmd.Env, "GOOS="+opts.GOOS)
	}
	if opts.GOARCH != "" {
		cmd.Env = append(cmd.Env, "GOARCH="+opts.GOARCH)
	}
	for k, v := range opts.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Capture rather than stream — mirrors the docker build pass
	// (crucible.go executeBuildPass: bx.Stdout = &stdoutBuf). `go build` emits a
	// flood of "go: downloading …" lines on a cold module cache; an engine must
	// never write framed output into the live stream (that breaks the Section box
	// the presentation layer is composing). We capture into a buffer, drop it on
	// success, and surface only the real diagnostics — via the returned error —
	// on failure. The presentation layer owns all framing.
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if g.Verbose {
		fmt.Fprintf(g.Stderr, "exec: %s %s\n", goBin, strings.Join(args, " "))
	}

	runErr := cmd.Run()

	if runErr != nil {
		// Surface the real diagnostics (compile errors), never the download spam.
		raw := buf.String()
		var diagLines []string
		for _, ln := range strings.Split(raw, "\n") {
			if strings.HasPrefix(strings.TrimSpace(ln), "go: downloading ") {
				continue // module-download chatter — never a diagnostic
			}
			if strings.TrimSpace(ln) != "" {
				diagLines = append(diagLines, ln)
			}
		}
		if diag := strings.TrimSpace(strings.Join(diagLines, "\n")); diag != "" {
			return nil, fmt.Errorf("go build failed: %w\n%s", runErr, diag)
		}
		return nil, fmt.Errorf("go build failed: %w", runErr)
	}

	// Compute size and checksum
	info, err := os.Stat(opts.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("stat output: %w", err)
	}

	hash, err := fileSHA256(opts.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("checksum output: %w", err)
	}

	absPath, _ := filepath.Abs(opts.OutputPath)

	return &GoBuildResult{
		Path:   absPath,
		Size:   info.Size(),
		SHA256: hash,
	}, nil
}

// DetectMainPackages scans a Go project for main packages.
// Looks in: cmd/*/main.go, */main.go, main.go
func (g *GoBuild) DetectMainPackages(rootDir string) ([]string, error) {
	var mains []string

	// Check cmd/*/main.go (most common Go project layout)
	cmdEntries, err := os.ReadDir(filepath.Join(rootDir, "cmd"))
	if err == nil {
		for _, entry := range cmdEntries {
			if !entry.IsDir() {
				continue
			}
			mainPath := filepath.Join("cmd", entry.Name(), "main.go")
			if _, err := os.Stat(filepath.Join(rootDir, mainPath)); err == nil {
				mains = append(mains, filepath.Join("cmd", entry.Name()))
			}
		}
	}

	// Check */main.go (single-level subdirectories)
	topEntries, err := os.ReadDir(rootDir)
	if err == nil {
		for _, entry := range topEntries {
			if !entry.IsDir() || entry.Name() == "cmd" || entry.Name() == "vendor" {
				continue
			}
			mainPath := filepath.Join(entry.Name(), "main.go")
			if _, err := os.Stat(filepath.Join(rootDir, mainPath)); err == nil {
				mains = append(mains, entry.Name())
			}
		}
	}

	// Check root main.go
	if _, err := os.Stat(filepath.Join(rootDir, "main.go")); err == nil {
		mains = append(mains, ".")
	}

	return mains, nil
}

// ToolchainVersion returns the Go toolchain version string.
func (g *GoBuild) ToolchainVersion(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "go", "version").Output()
	if err != nil {
		return "", fmt.Errorf("go version: %w", err)
	}
	// "go version go1.24.1 linux/amd64" → "go1.24.1"
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) >= 3 {
		return fields[2], nil
	}
	return strings.TrimSpace(string(out)), nil
}

// fileSHA256 computes the SHA-256 hex digest of a file.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
