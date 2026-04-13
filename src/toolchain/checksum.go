package toolchain

import (
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

var hexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// fetchChecksumFromURL downloads a checksum file and extracts the SHA256 hash.
//
// Supports two formats:
//   - Multi-line: "sha256  filename" per line — exact filename match, exactly ONE match required
//   - Single-hash: just the hex hash (64 chars) — no filename, entire content is the hash
//
// Parsing rules (non-negotiable):
//   - Exact filename match (not substring) for multi-line files
//   - Exactly ONE match — 0 matches = error, >1 matches = error
//   - Hex length must be exactly 64 characters
//   - Ambiguity = hard error
func fetchChecksumFromURL(checksumURL, targetFilename string) (string, error) {
	resp, err := httpGet(checksumURL)
	if err != nil {
		return "", fmt.Errorf("fetching checksum from %s: %w", checksumURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("checksum URL %s: HTTP %d", checksumURL, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading checksum from %s: %w", checksumURL, err)
	}

	content := strings.TrimSpace(string(body))
	if content == "" {
		return "", fmt.Errorf("checksum file %s is empty", checksumURL)
	}

	// Try single-hash format first (entire content is one hash)
	if hexPattern.MatchString(content) {
		return content, nil
	}

	// Multi-line format: "hash  filename" or "hash filename"
	baseFilename := filepath.Base(targetFilename)
	var matches []string

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Split on whitespace: hash + filename
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		hash := parts[0]
		filename := filepath.Base(strings.TrimPrefix(parts[len(parts)-1], "*")) // strip BSD-style * prefix

		// Exact match only — not substring
		if filename == baseFilename {
			if !hexPattern.MatchString(hash) {
				return "", fmt.Errorf("checksum for %s has invalid hash format: %q", baseFilename, hash)
			}
			matches = append(matches, hash)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no checksum found for %s in %s", baseFilename, checksumURL)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous: %d checksum entries for %s in %s", len(matches), baseFilename, checksumURL)
	}
}
