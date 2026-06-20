package lint

import (
	"bytes"
	"unicode/utf8"
)

// ContentKind classifies a file's bytes so lint modules route correctly. Only Text
// gets the text-oriented checks (unicode, lineendings) — an "invalid UTF-8" finding
// on a real image is noise, not signal. Binary and Ambiguous skip those checks but
// are STILL scanned by byte-oriented modules (secrets, entropy), so classification
// can never become an evasion path: a payload doesn't escape inspection by being
// (or pretending to be) binary.
type ContentKind int

const (
	ContentText      ContentKind = iota // genuine text → run text modules
	ContentBinary                       // binary blob → skip text modules, scan bytes
	ContentAmbiguous                    // UTF-16 / mixed / uncertain → skip text modules
)

// Content is the classification result — the shared primitive other modules route on.
// Magic is a sniffed type label ("png", "gzip", …) or "" when unknown; it drives
// extension/content mismatch detection.
type Content struct {
	Kind  ContentKind
	Magic string
}

// IsText reports whether the text-oriented modules should run. True ONLY for genuine
// text: Ambiguous is deliberately not treated as text, because parser-differential
// attacks live in exactly that boundary. The zero value is ContentText, so a file
// whose content was never classified defaults to text (the pre-classification
// behavior) — absence of classification never silently suppresses a check.
func (c Content) IsText() bool { return c.Kind == ContentText }

const classifyPrefix = 8192

// classifyContent classifies a prefix of file bytes via several independent signals —
// magic sniff, NUL bytes, UTF-16 BOM, UTF-8 validity, and control-char distribution —
// so no single heuristic can be gamed (UTF-16 or compressed-text payloads aren't
// mislabeled, an embedded NUL isn't missed, and "valid UTF-8 but noisy" doesn't pass
// as clean text).
func classifyContent(data []byte) Content {
	if len(data) == 0 {
		return Content{Kind: ContentText}
	}
	prefix := data
	if len(prefix) > classifyPrefix {
		prefix = prefix[:classifyPrefix]
	}
	// Magic signatures are a strong POSITIVE enrichment that also names the type for
	// extension-mismatch detection. Hand-rolled + auditable on purpose: the binary/text
	// decision below does NOT depend on it, so no third-party code is load-bearing in a
	// security control. A magic hit only confirms binary + labels the type.
	if m := sniffMagic(prefix); m != "" {
		return Content{Kind: ContentBinary, Magic: m}
	}
	// UTF-16 BOM is checked BEFORE the NUL scan: UTF-16 text legitimately contains NUL
	// bytes, so it must be recognized as an ambiguous textual encoding rather than
	// dismissed as binary (while still skipping the UTF-8 line checks).
	if len(prefix) >= 2 && ((prefix[0] == 0xFF && prefix[1] == 0xFE) || (prefix[0] == 0xFE && prefix[1] == 0xFF)) {
		return Content{Kind: ContentAmbiguous, Magic: "utf-16"}
	}
	// A NUL in a text window (no magic, no BOM) is a near-certain binary marker.
	if bytes.IndexByte(prefix, 0) >= 0 {
		return Content{Kind: ContentBinary}
	}
	// Rune-decode success RATIO, not all-or-nothing validity: partial validity is
	// where parser-differential smuggling hides. A mostly-text file with an embedded
	// binary chunk reads as "invalid" under utf8.Valid but ~0.95 here, landing it in
	// the scrutinized ambiguous middle instead of being silently dismissed as binary.
	validRatio := utf8ValidRatio(prefix)
	ctrl := controlRatio(prefix)
	switch {
	case validRatio > 0.99 && ctrl < 0.10:
		return Content{Kind: ContentText}
	case validRatio < 0.70 || ctrl > 0.30:
		return Content{Kind: ContentBinary}
	default:
		// Partially-valid or noisy: don't pretend it's clean text. Flag the boundary
		// as ambiguous; the anomaly layer (not the classifier) decides if it matters.
		return Content{Kind: ContentAmbiguous}
	}
}

// utf8ValidRatio is the fraction of bytes consumed by validly-decoding runes — a
// graded measure of textness, unlike utf8.Valid's all-or-nothing verdict.
func utf8ValidRatio(b []byte) float64 {
	if len(b) == 0 {
		return 1
	}
	valid := 0
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		valid += size
		i += size
	}
	return float64(valid) / float64(len(b))
}

// controlRatio is the fraction of bytes that are non-text control bytes — anything
// below 0x20 except the common whitespace (tab, LF, CR), plus DEL.
func controlRatio(b []byte) float64 {
	if len(b) == 0 {
		return 0
	}
	ctrl := 0
	for _, c := range b {
		if (c < 0x20 && c != '\t' && c != '\n' && c != '\r') || c == 0x7F {
			ctrl++
		}
	}
	return float64(ctrl) / float64(len(b))
}

// sniffMagic returns a short type label for known binary magic numbers, else "".
func sniffMagic(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case bytes.HasPrefix(b, []byte("\xFF\xD8\xFF")):
		return "jpeg"
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "gif"
	case len(b) >= 12 && bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "webp"
	case bytes.HasPrefix(b, []byte("%PDF-")):
		return "pdf"
	case bytes.HasPrefix(b, []byte("\x1F\x8B")):
		return "gzip"
	case bytes.HasPrefix(b, []byte("PK\x03\x04")), bytes.HasPrefix(b, []byte("PK\x05\x06")):
		return "zip"
	case bytes.HasPrefix(b, []byte("\x7FELF")):
		return "elf"
	case bytes.HasPrefix(b, []byte("ID3")):
		return "mp3"
	case bytes.HasPrefix(b, []byte("OggS")):
		return "ogg"
	case bytes.HasPrefix(b, []byte("BM")) && len(b) > 6:
		return "bmp"
	case bytes.HasPrefix(b, []byte("\xCA\xFE\xBA\xBE")), bytes.HasPrefix(b, []byte("\xFE\xED\xFA")):
		return "macho"
	case bytes.HasPrefix(b, []byte("\x28\xB5\x2F\xFD")):
		return "zstd"
	}
	return ""
}
