package docker

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
)

// crucibleBinaryPath must match the Dockerfile's final COPY destination.
const crucibleBinaryPath = "usr/local/bin/stagefreight"

// crucibleVersionTimeout bounds `stagefreight version` on an extracted binary.
const crucibleVersionTimeout = 30 * time.Second

// passArtifact is what the crucible compares for one pass, read from its OCI layout.
// The extracted binary runs on the runner: the crucible builds for the runner's platform.
type passArtifact struct {
	BinaryHash   string            // sha256 of the image's stagefreight binary
	Version      string            // that binary's `stagefreight version` output
	ConfigDigest string            // image config digest
	Labels       map[string]string // OCI config labels
}

// readPassArtifact reads one pass's artifact into workDir; every fact is required.
func readPassArtifact(ctx context.Context, layoutDir, workDir string) (passArtifact, error) {
	img, err := passLayoutImage(layoutDir)
	if err != nil {
		return passArtifact{}, err
	}
	cfgDigest, err := img.ConfigName()
	if err != nil {
		return passArtifact{}, fmt.Errorf("reading image config digest from %s: %w", layoutDir, err)
	}
	cf, err := img.ConfigFile()
	if err != nil {
		return passArtifact{}, fmt.Errorf("reading image config from %s: %w", layoutDir, err)
	}
	binPath, hash, err := extractCrucibleBinary(img, workDir)
	if err != nil {
		return passArtifact{}, fmt.Errorf("%s: %w", layoutDir, err)
	}
	version, err := runVersion(ctx, binPath)
	if err != nil {
		return passArtifact{}, err
	}
	return passArtifact{
		BinaryHash:   hash,
		Version:      version,
		ConfigDigest: cfgDigest.String(),
		Labels:       cf.Config.Labels,
	}, nil
}

// passLayoutImage resolves the pass's single runnable image, skipping attestation
// manifests (platform unknown/unknown); zero or several is an error.
func passLayoutImage(layoutDir string) (v1.Image, error) {
	p, err := layout.FromPath(layoutDir)
	if err != nil {
		return nil, fmt.Errorf("opening OCI layout %s: %w", layoutDir, err)
	}
	idx, err := p.ImageIndex()
	if err != nil {
		return nil, fmt.Errorf("reading OCI layout index %s: %w", layoutDir, err)
	}
	imgs, err := runnableImages(idx)
	if err != nil {
		return nil, fmt.Errorf("OCI layout %s: %w", layoutDir, err)
	}
	if len(imgs) != 1 {
		return nil, fmt.Errorf("OCI layout %s holds %d runnable images, want exactly 1", layoutDir, len(imgs))
	}
	return imgs[0], nil
}

// runnableImages collects the platform images in idx and its nested indexes.
func runnableImages(idx v1.ImageIndex) ([]v1.Image, error) {
	im, err := idx.IndexManifest()
	if err != nil {
		return nil, fmt.Errorf("reading index manifest: %w", err)
	}
	var out []v1.Image
	for _, d := range im.Manifests {
		if d.Platform != nil && d.Platform.OS == "unknown" {
			continue // attestation manifest
		}
		switch {
		case d.MediaType.IsIndex():
			sub, err := idx.ImageIndex(d.Digest)
			if err != nil {
				return nil, fmt.Errorf("reading nested index %s: %w", d.Digest, err)
			}
			nested, err := runnableImages(sub)
			if err != nil {
				return nil, err
			}
			out = append(out, nested...)
		case d.MediaType.IsImage():
			img, err := idx.Image(d.Digest)
			if err != nil {
				return nil, fmt.Errorf("reading image %s: %w", d.Digest, err)
			}
			out = append(out, img)
		}
	}
	return out, nil
}

// extractCrucibleBinary extracts and hashes the image's stagefreight binary into workDir.
func extractCrucibleBinary(img v1.Image, workDir string) (binPath, hash string, err error) {
	rc := mutate.Extract(img)
	defer rc.Close()
	tr := tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return "", "", fmt.Errorf("/%s is not in the image", crucibleBinaryPath)
		}
		if err != nil {
			return "", "", fmt.Errorf("reading image filesystem: %w", err)
		}
		if path.Clean(strings.TrimPrefix(h.Name, "./")) != crucibleBinaryPath {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return "", "", fmt.Errorf("/%s is not a regular file in the image", crucibleBinaryPath)
		}
		f, err := os.CreateTemp(workDir, "stagefreight-*")
		if err != nil {
			return "", "", fmt.Errorf("staging extracted binary: %w", err)
		}
		sum := sha256.New()
		_, copyErr := io.Copy(io.MultiWriter(f, sum), tr)
		closeErr := f.Close()
		if copyErr != nil {
			return "", "", fmt.Errorf("extracting /%s: %w", crucibleBinaryPath, copyErr)
		}
		if closeErr != nil {
			return "", "", fmt.Errorf("extracting /%s: %w", crucibleBinaryPath, closeErr)
		}
		if err := os.Chmod(f.Name(), 0o755); err != nil {
			return "", "", fmt.Errorf("marking extracted binary executable: %w", err)
		}
		return f.Name(), hex.EncodeToString(sum.Sum(nil)), nil
	}
}

// runVersion runs `<bin> version` with a minimal environment.
func runVersion(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, crucibleVersionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "version")
	cmd.Dir = filepath.Dir(bin)
	cmd.Env = []string{"HOME=" + filepath.Dir(bin), "PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil {
		detail := err.Error()
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			detail = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("running the extracted binary (`stagefreight version`): %s", detail)
	}
	return strings.TrimSpace(string(out)), nil
}
