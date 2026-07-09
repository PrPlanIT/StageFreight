package pages

import (
	"encoding/base64"
	"encoding/hex"
	"path/filepath"
	"strings"

	"lukechampine.com/blake3"
)

// AssetHasher computes the content hash a static-hosting provider's upload protocol
// expects for an asset. Isolated behind an interface so a provider whose protocol
// evolves gets a new versioned implementation (…V2Hasher) rather than a rewrite.
type AssetHasher interface {
	// Hash returns the provider's asset hash for a file. name carries the extension
	// (some protocols fold it into the hash); contents is the raw file bytes.
	Hash(name string, contents []byte) string
}

// cloudflarePagesV1Hasher is a faithful port of wrangler's hashFile
// (@cloudflare/deploy-helpers/src/deploy/helpers/hash.ts):
//
//	hashFile = blake3(base64(contents) + extensionWithoutDot).hex().slice(0, 32)
//
// Ported from the normative source, not reverse-engineered, and pinned by conformance
// vectors from wrangler's own test suite (cloudflare_hash_test.go).
type cloudflarePagesV1Hasher struct{}

func (cloudflarePagesV1Hasher) Hash(name string, contents []byte) string {
	b64 := base64.StdEncoding.EncodeToString(contents)
	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	h := blake3.New(32, nil)
	_, _ = h.Write([]byte(b64 + ext))
	return hex.EncodeToString(h.Sum(nil))[:32]
}
