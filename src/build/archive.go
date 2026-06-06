package build

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/atomicfile"
)

// DistDir is the StageFreight-managed output root for binary builds and their
// archives, relative to the repo root. It lives under .stagefreight/ on purpose:
// CI forwards only .stagefreight/ across the perform→publish job boundary, and
// the publish phase must be able to open the archive files its manifest
// references. Binaries, archives, and SHA256SUMS all live beneath this single
// root — splitting them (e.g. a bare top-level dist/) reopens the seam where
// publish receives metadata for files it does not possess.
const DistDir = ".stagefreight/dist"

// ArchiveOpts holds configuration for archive creation.
type ArchiveOpts struct {
	// Format is "tar.gz", "zip", or "auto" (zip for windows, tar.gz otherwise).
	Format string

	// OutputDir is where archives are written.
	OutputDir string

	// NameTemplate is the archive filename template (without extension).
	// Supports: {id}, {version}, {os}, {arch}
	NameTemplate string

	// BinaryPath is the path to the compiled binary to include.
	BinaryPath string

	// BinaryName is the filename inside the archive (may differ from physical path basename).
	BinaryName string

	// IncludeFiles lists extra files to bundle (relative to repo root).
	IncludeFiles []string

	// RepoRoot is the root directory for resolving include files.
	RepoRoot string

	// Platform is the target platform for this archive.
	Platform Platform

	// BuildID is used for template resolution.
	BuildID string

	// Version info for template resolution.
	Version *VersionInfo
}

// ArchiveResult holds the output of an archive operation.
type ArchiveResult struct {
	Path     string   // archive file path
	Format   string   // actual format used
	Size     int64
	SHA256   string
	Contents []string // files inside the archive
}

// CreateArchive builds an archive containing a binary and optional extra files.
// CreateArchive produces a deterministic archive file from the supplied
// binary + optional include files. Determinism rule (load-bearing for
// ArchiveOutcome.SHA256 stability across runs):
//
//   Archive identity MUST be deterministic over:
//   - sorted file set (canonical archive-name order, not caller order)
//   - normalized tar/zip headers (zero ModTime, zero Uid/Gid, fixed mode)
//   - single-pass SHA256 over the final stream
//
//   Any deviation introduces cross-run instability in ArchiveOutcome.SHA256.
//   Caller-supplied include order is intentionally ignored; sorting happens
//   here once, at the determinism boundary, not at every consumer.
func CreateArchive(opts ArchiveOpts) (*ArchiveResult, error) {
	// Resolve format
	format := opts.Format
	if format == "" || format == "auto" {
		if opts.Platform.OS == "windows" {
			format = "zip"
		} else {
			format = "tar.gz"
		}
	}

	// Resolve archive name
	name := resolveArchiveName(opts.NameTemplate, opts.BuildID, opts.Platform, opts.Version)
	ext := "." + format
	archivePath := filepath.Join(opts.OutputDir, name+ext)

	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	// Collect files to archive: binary first, then includes. Order at this
	// point is caller-supplied; canonicalization happens via sort below
	// before any byte is written.
	var entries []archiveEntry

	entries = append(entries, archiveEntry{
		sourcePath:  opts.BinaryPath,
		archiveName: opts.BinaryName,
	})

	for _, inc := range opts.IncludeFiles {
		srcPath := filepath.Join(opts.RepoRoot, inc)
		if _, err := os.Stat(srcPath); err != nil {
			return nil, fmt.Errorf("include file %q: %w", inc, err)
		}
		entries = append(entries, archiveEntry{
			sourcePath:  srcPath,
			archiveName: inc,
		})
	}

	// Determinism step 1: canonical file order. Sort by archive-name (the
	// in-archive path), not by source-path — same archive contents must
	// produce the same on-disk bytes regardless of where source files lived.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].archiveName < entries[j].archiveName
	})

	var contents []string
	for _, e := range entries {
		contents = append(contents, e.archiveName)
	}

	// Create archive
	switch format {
	case "tar.gz":
		if err := createTarGz(archivePath, entries); err != nil {
			return nil, err
		}
	case "zip":
		if err := createZip(archivePath, entries); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported archive format: %s", format)
	}

	// Get size and checksum
	info, err := os.Stat(archivePath)
	if err != nil {
		return nil, err
	}
	hash, err := fileSHA256(archivePath)
	if err != nil {
		return nil, err
	}

	return &ArchiveResult{
		Path:     archivePath,
		Format:   format,
		Size:     info.Size(),
		SHA256:   hash,
		Contents: contents,
	}, nil
}

// WriteChecksums creates a SHA256SUMS file from a set of archive results.
func WriteChecksums(outputDir string, archives []*ArchiveResult) (string, error) {
	checksumPath := filepath.Join(outputDir, "SHA256SUMS")

	// Sort by filename for determinism
	sorted := make([]*ArchiveResult, len(archives))
	copy(sorted, archives)
	sort.Slice(sorted, func(i, j int) bool {
		return filepath.Base(sorted[i].Path) < filepath.Base(sorted[j].Path)
	})

	var lines []string
	for _, a := range sorted {
		lines = append(lines, fmt.Sprintf("%s  %s", a.SHA256, filepath.Base(a.Path)))
	}

	content := strings.Join(lines, "\n") + "\n"
	if err := atomicfile.WriteFile(checksumPath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing checksums: %w", err)
	}

	return checksumPath, nil
}

func resolveArchiveName(tmpl, buildID string, plat Platform, v *VersionInfo) string {
	if tmpl == "" {
		tmpl = "{id}-{version}-{os}-{arch}"
	}
	s := tmpl
	s = strings.ReplaceAll(s, "{id}", buildID)
	s = strings.ReplaceAll(s, "{os}", plat.OS)
	s = strings.ReplaceAll(s, "{arch}", plat.Arch)
	if v != nil {
		s = strings.ReplaceAll(s, "{version}", v.Version)
	}
	return s
}

type archiveEntry struct {
	sourcePath  string
	archiveName string
}

func createTarGz(outputPath string, entries []archiveEntry) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("creating tar.gz: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	// gzip header normalization: ModTime defaults to wall clock and OS byte
	// defaults to runtime OS — both leak into the archive bytes. Zero them
	// for cross-run / cross-host hash stability.
	gw.ModTime = time.Time{}
	gw.OS = 0
	gw.Name = ""
	gw.Comment = ""
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	for _, entry := range entries {
		if err := addToTar(tw, entry.sourcePath, entry.archiveName); err != nil {
			return fmt.Errorf("adding %s to tar: %w", entry.archiveName, err)
		}
	}

	return nil
}

// tarEpoch is the fixed timestamp used in tar entry headers. Any value would
// do; zero is the natural choice and matches gzip header normalization.
var tarEpoch = time.Time{}

func addToTar(tw *tar.Writer, sourcePath, archiveName string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}

	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = archiveName

	// tar entry normalization: every field that can vary across hosts /
	// runs / users is forced to a fixed value. Mode preserves only the
	// executable bit (0755 for executables, 0644 for everything else) —
	// host-specific permission bits would otherwise leak into the hash.
	header.ModTime = tarEpoch
	header.AccessTime = tarEpoch
	header.ChangeTime = tarEpoch
	header.Uid = 0
	header.Gid = 0
	header.Uname = ""
	header.Gname = ""
	if info.Mode()&0o111 != 0 {
		header.Mode = 0o755
	} else {
		header.Mode = 0o644
	}

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	f, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(tw, f)
	return err
}

func createZip(outputPath string, entries []archiveEntry) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("creating zip: %w", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	for _, entry := range entries {
		if err := addToZip(zw, entry.sourcePath, entry.archiveName); err != nil {
			return fmt.Errorf("adding %s to zip: %w", entry.archiveName, err)
		}
	}

	return nil
}

func addToZip(zw *zip.Writer, sourcePath, archiveName string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = archiveName
	header.Method = zip.Deflate

	// zip entry normalization: Modified is the only timestamp zip carries
	// natively, but it defaults to file mtime → variable. Mode/permission
	// bits are absent from zip headers in most cases (FAT/external attrs
	// would carry them if set; we don't).
	header.Modified = tarEpoch

	w, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}

	f, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(w, f)
	return err
}

// ChecksumFile computes the SHA-256 hex digest of a file and writes a .sha256 sidecar.
func ChecksumFile(path string) (string, error) {
	hexDigest, err := fileSHA256(path)
	if err != nil {
		return "", err
	}

	sidecar := path + ".sha256"
	content := fmt.Sprintf("%s  %s\n", hexDigest, filepath.Base(path))
	if err := atomicfile.WriteFile(sidecar, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing checksum sidecar: %w", err)
	}

	return hexDigest, nil
}
