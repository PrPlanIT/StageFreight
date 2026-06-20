package docker

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/build/domains"
)

// stageBuildBinaries recycles binaries produced by earlier binary builds into docker
// build contexts, so a copy-pre-built Dockerfile (COPY <name>) resolves — the compiled
// binary is reused instead of recompiled inside the image. Driven by each docker
// build's `stage: {from, as}`: `from` names the binary build, `as` the destination
// within the context, with {arch}/{os} substituted (Docker's TARGETARCH naming) so a
// multi-arch Dockerfile gets the right binary per platform.
//
// Safe to run before buildx: the binary contributor (Order 10) has already populated
// rc.Outputs by the time the image contributor (Order 15) builds, so every staged
// binary is on disk.
func stageBuildBinaries(rc *domains.RunContext) error {
	if rc.Config == nil || rc.Outputs == nil {
		return nil
	}
	for i := range rc.Config.Builds {
		b := rc.Config.Builds[i]
		if b.Kind != "docker" || b.Stage == nil {
			continue
		}
		ctx := b.Context
		if ctx == "" {
			ctx = "."
		}
		staged := 0
		for _, art := range rc.Outputs.Artifacts {
			if art.Binary == nil || art.Binary.BuildID != b.Stage.From {
				continue
			}
			dest := substituteStageName(b.Stage.As, art.Binary.OS, art.Binary.Arch)
			if err := copyInto(art.Binary.Path, filepath.Join(ctx, dest)); err != nil {
				return fmt.Errorf("stage for docker build %q: %w", b.ID, err)
			}
			staged++
		}
		if staged == 0 {
			return fmt.Errorf("docker build %q stages from %q, which produced no binary "+
				"(did that binary build run and succeed?)", b.ID, b.Stage.From)
		}
	}
	return nil
}

// substituteStageName resolves {arch}/{os} placeholders, e.g. "app-{arch}" → "app-amd64".
func substituteStageName(pattern, os, arch string) string {
	return strings.NewReplacer("{arch}", arch, "{os}", os).Replace(pattern)
}

// copyInto copies src to dst (creating parents), preserving the executable bit.
func copyInto(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
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
