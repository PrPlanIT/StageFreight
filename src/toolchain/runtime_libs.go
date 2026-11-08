package toolchain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// runtimeLibDir is the directory inside a toolchain tree that holds the shared
// libraries provisioned for it; the tool runs with LD_LIBRARY_PATH pointing here.
const runtimeLibDir = "runtime-lib"

// runtimeLibEnv is the environment a tool needs to load libraries from libDir.
func runtimeLibEnv(libDir string) []string {
	return []string{"LD_LIBRARY_PATH=" + libDir}
}

// provisionRuntimeLibs installs into libDir every shared library binPath needs that the
// host's loader cannot find, following each provisioned library's own needs in turn.
// It returns the regular files placed (name → SHA-256) and the packages they came
// from. Any library that cannot be sourced and verified is an error naming it.
func provisionRuntimeLibs(binPath, libDir string) (map[string]string, []string, error) {
	queue, err := elfNeeded(binPath)
	if err != nil {
		return nil, nil, err
	}
	missing := missingLibraries(queue, libDir)
	if len(missing) == 0 {
		return nil, nil, nil
	}
	names := strings.Join(missing, ", ")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", names, err)
	}
	repo, err := hostMainRepo()
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", names, err)
	}
	index, err := fetchAPKIndex(repo)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", names, err)
	}
	var sources []string
	fetched := map[string]bool{}
	for len(queue) > 0 {
		so := queue[0]
		queue = queue[1:]
		if len(missingLibraries([]string{so}, libDir)) == 0 {
			continue
		}
		pkg, err := index.provider(so)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %s: %w", so, repo.url, err)
		}
		if fetched[pkg.name] {
			return nil, nil, fmt.Errorf("%s: package %s claims to provide it but does not ship it", so, pkg.id())
		}
		fetched[pkg.name] = true
		payload, err := fetchAPKData(repo, pkg)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", so, err)
		}
		placed, err := extractSharedLibs(payload, libDir)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %s: %w", so, pkg.id(), err)
		}
		sources = append(sources, repo.fileURL(pkg.id()+".apk"))
		for _, name := range placed {
			needs, err := elfNeeded(filepath.Join(libDir, name))
			if err != nil {
				return nil, nil, err
			}
			queue = append(queue, needs...)
		}
		queue = append(queue, so) // re-checked: must now resolve, or the loop names it
	}
	libs, err := hashRegularFiles(libDir)
	if err != nil {
		return nil, nil, err
	}
	return libs, sources, nil
}

// runtimeLibsIntact reports whether libDir still holds exactly the recorded libraries
// and, with them, satisfies every library binPath and those libraries need.
func runtimeLibsIntact(binPath, libDir string, recorded map[string]string) bool {
	for name, want := range recorded {
		if got, err := fileSHA256(filepath.Join(libDir, name)); err != nil || got != want {
			return false
		}
	}
	needs, err := elfNeeded(binPath)
	if err != nil {
		return false
	}
	for name := range recorded {
		more, err := elfNeeded(filepath.Join(libDir, name))
		if err != nil {
			return false
		}
		needs = append(needs, more...)
	}
	return len(missingLibraries(needs, libDir)) == 0
}

// missingLibraries returns the names in needs that neither libDir nor the host's
// default library directories provide.
func missingLibraries(needs []string, libDir string) []string {
	var missing []string
	for _, so := range needs {
		found := false
		for _, dir := range append([]string{libDir}, hostLibraryDirs...) {
			if _, err := os.Stat(filepath.Join(dir, so)); err == nil {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, so)
		}
	}
	return missing
}

// elfNeeded returns the DT_NEEDED entries of an ELF file.
func elfNeeded(file string) ([]string, error) {
	f, err := elf.Open(file)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	defer f.Close()
	needs, err := f.ImportedLibraries()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	return needs, nil
}

// extractSharedLibs writes the shared libraries in a package's data section (regular
// files and symlinks directly under lib/ or usr/lib/) into libDir, flat. It returns
// the regular files written. Symlinks must point at a sibling.
func extractSharedLibs(payload []byte, libDir string) ([]string, error) {
	zr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	var placed []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		dir, name := path.Split(path.Clean(hdr.Name))
		if (dir != "lib/" && dir != "usr/lib/") || !strings.Contains(name, ".so") {
			continue
		}
		dst := filepath.Join(libDir, name)
		switch hdr.Typeflag {
		case tar.TypeReg:
			if err := writeFileAtomic(dst, io.LimitReader(tr, maxAPKDownload)); err != nil {
				return nil, err
			}
			placed = append(placed, name)
		case tar.TypeSymlink:
			if strings.ContainsAny(hdr.Linkname, "/\\") {
				return nil, fmt.Errorf("%s links outside its directory (%s)", name, hdr.Linkname)
			}
			os.Remove(dst)
			if err := os.Symlink(hdr.Linkname, dst); err != nil {
				return nil, err
			}
		}
	}
	if len(placed) == 0 {
		return nil, fmt.Errorf("ships no shared library")
	}
	return placed, nil
}

func writeFileAtomic(dst string, r io.Reader) error {
	tmp := dst + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// hashRegularFiles returns name → SHA-256 for the regular files in dir.
func hashRegularFiles(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		sum, err := fileSHA256(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out[e.Name()] = sum
	}
	return out, nil
}

// splitGzipMembers returns the raw bytes of each concatenated gzip member. An apk
// signature covers a member's compressed bytes, so the boundaries must be exact:
// bytes.Reader is an io.ByteReader, so the decompressor never reads past a member.
func splitGzipMembers(data []byte) ([][]byte, error) {
	var members [][]byte
	for off := 0; off < len(data); {
		r := bytes.NewReader(data[off:])
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("gzip section %d: %w", len(members)+1, err)
		}
		zr.Multistream(false)
		if _, err := io.Copy(io.Discard, zr); err != nil {
			return nil, fmt.Errorf("gzip section %d: %w", len(members)+1, err)
		}
		n := len(data) - off - r.Len()
		members = append(members, data[off:off+n])
		off += n
	}
	return members, nil
}

// readTarFile returns the contents of the named entry in a gzip-compressed tar.
func readTarFile(member []byte, name string) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(member))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err != nil {
			return nil, fmt.Errorf("no %s entry: %w", name, err)
		}
		if hdr.Name == name {
			return io.ReadAll(io.LimitReader(tr, maxAPKDownload))
		}
	}
}

// httpGetBytes fetches a URL into memory, bounded by maxAPKDownload.
func httpGetBytes(url string) ([]byte, error) {
	resp, err := httpGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: HTTP %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAPKDownload+1))
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	if len(data) > maxAPKDownload {
		return nil, fmt.Errorf("fetching %s: larger than %d bytes", url, maxAPKDownload)
	}
	return data, nil
}
