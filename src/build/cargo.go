package build

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CargoBuild wraps Cargo compilation — the Rust analog of GoBuild. It runs
// `cargo build` and copies the produced binary to the canonical output path, so a
// Rust binary lands in the SAME artifact lifecycle (DistDir layout, SHA256, archive,
// signing) as a Go binary. The build's "how" differs (cargo vs go); the artifact
// contract is identical.
type CargoBuild struct {
	Verbose bool
	Stdout  io.Writer
	Stderr  io.Writer
}

func NewCargoBuild(verbose bool) *CargoBuild {
	return &CargoBuild{Verbose: verbose, Stdout: os.Stdout, Stderr: os.Stderr}
}

// CargoBuildOpts holds the parameters for a single cargo compilation.
type CargoBuildOpts struct {
	ManifestDir string            // dir containing Cargo.toml (cmd.Dir)
	BinName     string            // the [[bin]]/package name → target/<profile>/<BinName>
	OutputPath  string            // canonical output (DistDir/<slug>/<name>) — the binary is copied here
	Release     bool              // --release (release profile)
	TargetDir   string            // CARGO_TARGET_DIR (where target/ lives); "" = ManifestDir/target
	Args        []string          // raw args before nothing (e.g. ["--features", "x"])
	Env         map[string]string // additional env (e.g. CARGO_HOME)
	CargoBin    string            // resolved cargo binary; "" falls back to "cargo" on $PATH (dev only)
}

// CargoBuildResult holds the output of a cargo compilation.
type CargoBuildResult struct {
	Path   string
	Size   int64
	SHA256 string
}

// Build compiles a Rust binary and copies it to OutputPath.
func (c *CargoBuild) Build(ctx context.Context, opts CargoBuildOpts) (*CargoBuildResult, error) {
	args := []string{"build"}
	if opts.Release {
		args = append(args, "--release")
	}
	if opts.BinName != "" {
		args = append(args, "--bin", opts.BinName)
	}
	args = append(args, opts.Args...)

	cargoBin := opts.CargoBin
	if cargoBin == "" {
		cargoBin = "cargo"
	}
	cmd := exec.CommandContext(ctx, cargoBin, args...)
	cmd.Dir = opts.ManifestDir

	// Overrides must win over inherited env (same first-occurrence hazard as GoBuild).
	overrides := map[string]string{}
	if opts.TargetDir != "" {
		overrides["CARGO_TARGET_DIR"] = opts.TargetDir
	}
	for k, v := range opts.Env {
		overrides[k] = v
	}
	cmd.Env = composeEnv(os.Environ(), overrides)

	// Capture (cargo is chatty: "Compiling …", "Downloading …"); surface only real
	// diagnostics on failure, never into the live presentation stream.
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if c.Verbose {
		fmt.Fprintf(c.Stderr, "exec: %s %s (in %s)\n", cargoBin, strings.Join(args, " "), opts.ManifestDir)
	}
	if err := cmd.Run(); err != nil {
		if diag := strings.TrimSpace(buf.String()); diag != "" {
			return nil, fmt.Errorf("cargo build failed: %w\n%s", err, diag)
		}
		return nil, fmt.Errorf("cargo build failed: %w", err)
	}

	// Locate the produced binary: <targetDir>/<profile>/<BinName>.
	targetDir := opts.TargetDir
	if targetDir == "" {
		targetDir = filepath.Join(opts.ManifestDir, "target")
	}
	profile := "debug"
	if opts.Release {
		profile = "release"
	}
	builtPath := filepath.Join(targetDir, profile, opts.BinName)
	if _, err := os.Stat(builtPath); err != nil {
		return nil, fmt.Errorf("cargo build: produced binary not found at %s (check the [[bin]]/package name): %w", builtPath, err)
	}

	// Copy into the canonical artifact path (same DistDir layout as Go).
	if err := os.MkdirAll(filepath.Dir(opts.OutputPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating output directory: %w", err)
	}
	if err := copyExecutable(builtPath, opts.OutputPath); err != nil {
		return nil, fmt.Errorf("copying built binary: %w", err)
	}

	info, err := os.Stat(opts.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("stat output: %w", err)
	}
	hash, err := fileSHA256(opts.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("checksum output: %w", err)
	}
	absPath, _ := filepath.Abs(opts.OutputPath)
	return &CargoBuildResult{Path: absPath, Size: info.Size(), SHA256: hash}, nil
}

// copyExecutable copies src to dst preserving the executable bit.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
