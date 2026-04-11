package toolchain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const metadataFile = ".metadata.json"

// Metadata is the provenance record for a cached toolchain install.
// Immutable after write. If missing or checksum mismatch, the install
// is treated as corrupt and re-downloaded.
type Metadata struct {
	Tool      string `json:"tool"`
	Version   string `json:"version"`
	Platform  string `json:"platform"`
	SourceURL string `json:"source_url"`

	// SHA256 is the checksum of the downloaded archive (provenance).
	// Verified against official release checksums at download time.
	SHA256 string `json:"sha256"`

	// BinSHA256 is the checksum of the extracted binary (cache validation).
	// Used on cache hit to verify the binary hasn't been tampered with.
	BinSHA256 string `json:"bin_sha256"`

	InstalledAt string `json:"installed_at"`
	InstalledBy string `json:"installed_by"`
}

// ReadMetadata reads the provenance record for a cached toolchain.
func ReadMetadata(rootDir, tool, version string) (Metadata, error) {
	path := filepath.Join(CacheDir(rootDir, tool, version), metadataFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, err
	}
	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return Metadata{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return m, nil
}

// WriteMetadata writes the provenance record atomically (write to temp, rename).
func WriteMetadata(rootDir, tool, version string, m Metadata) error {
	m.InstalledAt = time.Now().UTC().Format(time.RFC3339)
	m.InstalledBy = "stagefreight/toolchain"

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := CacheDir(rootDir, tool, version)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	target := filepath.Join(dir, metadataFile)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
