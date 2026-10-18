package upstream

import (
	"archive/tar"
	"bufio"
	"bytes"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// maxProbeFile bounds how much of one file a probe reads.
const maxProbeFile = 4 << 20

// Probe reads the version a built image contains, without running it: from an OCI label, a
// file, or a package database. The result is the raw version text, before any scheme parse.
func Probe(img v1.Image, p config.UpstreamProbe) (string, error) {
	switch {
	case p.Label != "":
		cf, err := img.ConfigFile()
		if err != nil {
			return "", fmt.Errorf("reading image config: %w", err)
		}
		val := ""
		if cf != nil && cf.Config.Labels != nil {
			val = cf.Config.Labels[p.Label]
		}
		if val == "" {
			return "", fmt.Errorf("label %s is not set on the image", p.Label)
		}
		return extract(val, p.Match, "label "+p.Label)
	case p.File != "":
		files, err := readFiles(img, map[string]bool{clean(p.File): true})
		if err != nil {
			return "", err
		}
		content, ok := files[clean(p.File)]
		if !ok {
			return "", fmt.Errorf("%s is not a file in the image", p.File)
		}
		return extract(string(content), p.Match, p.File)
	case p.Package != "":
		return probePackage(img, p.Manager, p.Package)
	}
	return "", fmt.Errorf("probe sets none of label | file | package")
}

// extract applies match (default: the first dotted version) and returns the version group.
func extract(text, match, what string) (string, error) {
	if match == "" {
		match = config.DefaultProbeMatch
	}
	re, err := regexp.Compile(match)
	if err != nil {
		return "", err
	}
	m := re.FindStringSubmatch(text)
	if m == nil {
		return "", fmt.Errorf("%s: no match for %s", what, match)
	}
	switch vi := re.SubexpIndex("version"); {
	case vi > 0:
		return m[vi], nil
	case len(m) > 1:
		return m[1], nil
	}
	return m[0], nil
}

// clean normalizes an image path to the form flattened tar entries use (no leading / or ./).
func clean(p string) string {
	return strings.TrimPrefix(path.Clean("/"+p), "/")
}

// readFiles reads the wanted regular files from the flattened image filesystem, following one
// level of symlink (e.g. /etc/os-release → ../usr/lib/os-release).
func readFiles(img v1.Image, want map[string]bool) (map[string][]byte, error) {
	out := map[string][]byte{}
	links := map[string]string{}
	err := walk(img, func(h *tar.Header, r io.Reader) error {
		name := clean(h.Name)
		if !want[name] {
			return nil
		}
		switch h.Typeflag {
		case tar.TypeSymlink:
			target := h.Linkname
			if !strings.HasPrefix(target, "/") {
				target = path.Join(path.Dir("/"+name), target)
			}
			links[name] = clean(target)
		case tar.TypeReg:
			b, err := io.ReadAll(io.LimitReader(r, maxProbeFile))
			if err != nil {
				return err
			}
			out[name] = b
		}
		return nil
	})
	if err != nil || len(links) == 0 {
		return out, err
	}
	targets := map[string]bool{}
	for _, t := range links {
		targets[t] = true
	}
	resolved, err := readFiles(img, targets)
	if err != nil {
		return nil, err
	}
	for name, t := range links {
		if b, ok := resolved[t]; ok {
			out[name] = b
		}
	}
	return out, nil
}

// walk streams the flattened filesystem of the image (whiteouts applied).
func walk(img v1.Image, fn func(*tar.Header, io.Reader) error) error {
	rc := mutate.Extract(img)
	defer rc.Close()
	tr := tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading image filesystem: %w", err)
		}
		if err := fn(h, tr); err != nil {
			return err
		}
	}
}

// pipSep folds a distribution name the way dist-info directories spell it (PEP 503
// separators become _).
var pipSep = regexp.MustCompile(`[-_.]+`)

func probePackage(img v1.Image, manager, pkg string) (string, error) {
	switch manager {
	case "pip":
		name := pipSep.ReplaceAllString(strings.ToLower(pkg), "_")
		re := regexp.MustCompile(`(?:^|/)(?:site|dist)-packages/([^/]+)-([^/-]+)\.(?:dist|egg)-info(?:/|$)`)
		found := map[string]bool{}
		err := walk(img, func(h *tar.Header, _ io.Reader) error {
			if m := re.FindStringSubmatch(clean(h.Name)); m != nil && pipSep.ReplaceAllString(strings.ToLower(m[1]), "_") == name {
				found[m[2]] = true
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		return single(found, "pip package "+pkg)
	case "dpkg":
		return probeDB(img, "var/lib/dpkg/status", "Package: ", "Version: ", pkg, true)
	case "apk":
		return probeDB(img, "lib/apk/db/installed", "P:", "V:", pkg, false)
	}
	return "", fmt.Errorf("unknown package manager %q", manager)
}

// probeDB reads a stanza-per-package database (dpkg status, apk installed): the stanza whose
// name line is pkg yields its version line. Stanzas are separated by blank lines.
func probeDB(img v1.Image, file, nameKey, versionKey, pkg string, needInstalled bool) (string, error) {
	files, err := readFiles(img, map[string]bool{file: true})
	if err != nil {
		return "", err
	}
	db, ok := files[file]
	if !ok {
		return "", fmt.Errorf("/%s is not in the image", file)
	}
	found := map[string]bool{}
	var name, ver string
	installed := !needInstalled
	flush := func() {
		if name == pkg && ver != "" && installed {
			found[ver] = true
		}
		name, ver, installed = "", "", !needInstalled
	}
	sc := bufio.NewScanner(bytes.NewReader(db))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, nameKey):
			name = strings.TrimSpace(strings.TrimPrefix(line, nameKey))
		case strings.HasPrefix(line, versionKey):
			ver = strings.TrimSpace(strings.TrimPrefix(line, versionKey))
		case strings.HasPrefix(line, "Status: "):
			installed = strings.HasSuffix(line, " installed")
		}
	}
	flush()
	return single(found, file+" package "+pkg)
}

func single(found map[string]bool, what string) (string, error) {
	switch len(found) {
	case 0:
		return "", fmt.Errorf("%s is not installed in the image", what)
	case 1:
		for v := range found {
			return v, nil
		}
	}
	var vs []string
	for v := range found {
		vs = append(vs, v)
	}
	return "", fmt.Errorf("%s is installed more than once (%s)", what, strings.Join(vs, ", "))
}

// packageRevision is the packaging revision a distro adds to an upstream version: a dpkg epoch
// and Debian revision (1:2.8.1-1+deb12u1), an apk -rN (2.8.1-r3). It is the distro's, not
// upstream's, so it is removed before the version is compared with our claim.
var (
	dpkgEpoch    = regexp.MustCompile(`^\d+:`)
	dpkgRevision = regexp.MustCompile(`-[^-]+$`)
	apkRevision  = regexp.MustCompile(`-r\d+$`)
)

// UpstreamPart strips a package manager's own revision from a probed version.
func UpstreamPart(manager, v string) string {
	switch manager {
	case "dpkg":
		return dpkgRevision.ReplaceAllString(dpkgEpoch.ReplaceAllString(v, ""), "")
	case "apk":
		return apkRevision.ReplaceAllString(v, "")
	}
	return v
}
