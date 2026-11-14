package discovery

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// renovateMarker opens a Renovate-style annotation comment; only datasource=docker is read.
const renovateMarker = "renovate:"

// sourceImageRef is one docker annotation: Ref is <image>:<tag>[@digest] as written on
// Line, with the tag starting at byte Column. Skip says why the annotation is unusable.
type sourceImageRef struct {
	Image  string
	Ref    string
	Line   int
	Column int
	Skip   string
}

// checkSourceImages resolves container image references opted in by a
// `renovate: datasource=docker depName=<image>` comment, the line after which holds
// <image>:<tag>. Each resolves exactly like a Dockerfile base image. A line that already
// carries a Dockerfile image dependency is left to it.
func (m *Resolver) checkSourceImages(ctx context.Context, file lint.FileInfo, existing []supplychain.Dependency) ([]supplychain.Dependency, error) {
	if !m.cfg.SourceEnabled(supplychain.EcosystemSourceImage) {
		return nil, nil
	}
	content, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(content, []byte(renovateMarker)) || bytes.IndexByte(content[:min(len(content), 8192)], 0) >= 0 {
		return nil, nil
	}

	taken := map[int]bool{}
	for _, d := range existing {
		if d.Ecosystem == supplychain.EcosystemDockerImage {
			taken[d.Line] = true
		}
	}

	var deps []supplychain.Dependency
	for _, ref := range parseSourceImageRefs(content) {
		if taken[ref.Line] {
			continue
		}
		if ref.Skip != "" {
			deps = append(deps, supplychain.Dependency{
				Name:            ref.Image,
				Ecosystem:       supplychain.EcosystemSourceImage,
				File:            file.Path,
				Line:            ref.Line,
				ResolutionError: ref.Skip,
			})
			continue
		}
		stage := supplychain.StageInfo{Image: ref.Ref, Line: ref.Line}
		if dep := m.resolveImage(ctx, file.Path, stage, &supplychain.DockerFreshnessInfo{}); dep != nil {
			dep.Ecosystem = supplychain.EcosystemSourceImage
			dep.Column = ref.Column
			deps = append(deps, *dep)
		}
	}
	return deps, nil
}

// parseSourceImageRefs finds every renovate: datasource=docker annotation in content.
// Annotations for other datasources are not returned.
func parseSourceImageRefs(content []byte) []sourceImageRef {
	lines := strings.Split(string(content), "\n")
	var refs []sourceImageRef
	for i, l := range lines {
		at := strings.Index(l, renovateMarker)
		if at < 0 {
			continue
		}
		var datasource, image string
		for _, f := range strings.Fields(l[at+len(renovateMarker):]) {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			switch k {
			case "datasource":
				datasource = v
			case "depName":
				image = v
			}
		}
		if datasource != "docker" {
			continue
		}
		ref := sourceImageRef{Image: image, Line: i + 1}
		if image == "" || strings.Contains(image, "$") {
			ref.Skip = "renovate annotation has no literal depName"
			refs = append(refs, ref)
			continue
		}
		next := ""
		if i+1 < len(lines) {
			next = lines[i+1]
		}
		ref.Ref, ref.Column = findImageTag(next, image)
		if ref.Ref == "" {
			ref.Skip = fmt.Sprintf("line after the renovate annotation does not contain %s:<tag>", image)
		} else {
			ref.Line = i + 2
		}
		refs = append(refs, ref)
	}
	return refs
}

// findImageTag locates image:<tag>[@sha256:<hex>] in line, returning the reference and the
// tag's byte column. image must start the reference, not end a longer name.
func findImageTag(line, image string) (string, int) {
	prefix := image + ":"
	for off := 0; ; {
		idx := strings.Index(line[off:], prefix)
		if idx < 0 {
			return "", 0
		}
		start := off + idx
		col := start + len(prefix)
		end := col
		for end < len(line) && isTagByte(line[end]) {
			end++
		}
		if end > col && (start == 0 || !isImageNameByte(line[start-1])) {
			if strings.HasPrefix(line[end:], "@sha256:") {
				hex := end + len("@sha256:")
				d := hex
				for d < len(line) && isHexByte(line[d]) {
					d++
				}
				if d > hex {
					end = d
				}
			}
			return line[start:end], col
		}
		off = col
	}
}

func isTagByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-'
}

func isImageNameByte(c byte) bool {
	return isTagByte(c) || c == '/'
}

func isHexByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
}
