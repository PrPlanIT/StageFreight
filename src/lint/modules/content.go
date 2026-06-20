package modules

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("content", func() lint.Module { return &contentModule{} })
}

// contentModule is the NORMATIVE half of content handling: the classifier describes
// what a file is; this module fires ONLY when a file's content contradicts what its
// name claims, or carries a cheap, high-signal concealment marker. Honest text, honest
// binaries, and bare ambiguity (minified bundles, source maps, generated assets) emit
// nothing — they're listed in the ungraded disclosure inventory, not reported here.
// Ambiguity alone is never a finding; only an exploit-relevant violation is.
type contentModule struct{}

func (m *contentModule) Name() string         { return "content" }
func (m *contentModule) DefaultEnabled() bool { return true }
func (m *contentModule) AutoDetect() []string { return nil }

// CacheTTL is negative — never cache. The verdict depends on the file's NAME (its
// extension) versus its content, but the lint cache keys only on content, so caching
// would mis-share results across same-content / different-name files.
func (m *contentModule) CacheTTL() time.Duration { return -1 }

// textExtensions name files that MUST be text. Binary or partially-binary content
// behind one of these is the classic smuggling move (a payload named notes.md).
var textExtensions = map[string]bool{
	".md": true, ".txt": true, ".rs": true, ".go": true, ".js": true, ".ts": true,
	".jsx": true, ".tsx": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true,
	".css": true, ".scss": true, ".html": true, ".htm": true, ".sh": true, ".bash": true,
	".py": true, ".rb": true, ".c": true, ".h": true, ".cpp": true, ".hpp": true,
	".cc": true, ".java": true, ".kt": true, ".swift": true, ".php": true, ".sql": true,
	".xml": true, ".ini": true, ".cfg": true, ".conf": true, ".csv": true, ".tsv": true,
	".rst": true, ".tex": true, ".lua": true, ".pl": true, ".r": true,
}

// binaryExtExpect maps a binary extension to the magic label we EXPECT (matching the
// classifier's labels). A mismatch means the file is lying about its type. Extensions
// with no firm expectation (.bin, .dat, .o) are absent and never flagged.
var binaryExtExpect = map[string]string{
	".png": "png", ".jpg": "jpeg", ".jpeg": "jpeg", ".gif": "gif", ".webp": "webp",
	".bmp": "bmp", ".pdf": "pdf", ".gz": "gzip", ".tgz": "gzip", ".zip": "zip",
	".zst": "zstd", ".ogg": "ogg", ".mp3": "mp3",
}

// mediaDataExtensions must never carry executable script content; a shebang inside one
// is execution-path disguise (a dropper renamed to look like data/media).
var mediaDataExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true,
	".ico": true, ".pdf": true, ".mp3": true, ".ogg": true, ".wav": true, ".mp4": true,
	".mov": true, ".avi": true, ".zip": true, ".gz": true, ".tgz": true, ".tar": true,
	".zst": true, ".dat": true, ".bin": true, ".woff": true, ".woff2": true, ".ttf": true,
}

// pngIEND is the terminal 8 bytes of a well-formed PNG (the zero-length IEND chunk).
var pngIEND = []byte("IEND\xAE\x42\x60\x82")

func (m *contentModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	ext := strings.ToLower(filepath.Ext(file.Path))
	var out []lint.Finding

	// 1) A file that claims to be text but isn't.
	if textExtensions[ext] {
		switch {
		case file.Content.Kind == lint.ContentBinary:
			out = append(out, finding(file.Path, lint.SeverityCritical,
				"binary content in a text-named file (possible smuggling/obfuscation)"))
		case file.Content.Kind == lint.ContentAmbiguous && file.Content.Magic == "":
			// Partially-binary / non-text content where clean text was expected — the
			// embedded-binary-in-source case. (UTF-16 etc. carry a Magic label and are
			// not flagged.)
			out = append(out, finding(file.Path, lint.SeverityWarning,
				"partially-binary / non-text content in a text-named file"))
		}
	}

	// 2) A binary file whose magic contradicts its extension → disguised content.
	if want, ok := binaryExtExpect[ext]; ok && file.Content.Magic != "" && file.Content.Magic != want {
		out = append(out, finding(file.Path, lint.SeverityWarning,
			"content type ("+file.Content.Magic+") does not match extension "+ext))
	}

	// 3) Executable script disguised as data/media (a shebang where none belongs).
	if mediaDataExtensions[ext] {
		if head := readHead(file.AbsPath, 2); bytes.HasPrefix(head, []byte("#!")) {
			out = append(out, finding(file.Path, lint.SeverityCritical,
				"executable script (shebang) disguised as "+ext+" — execution-path deception"))
		}
	}

	// 4) Data appended past a PNG's IEND terminator (classic stego/smuggling). Cheap:
	// a clean PNG ends in exactly these 8 bytes.
	if file.Content.Magic == "png" {
		if tail := readTail(file.AbsPath, len(pngIEND)); len(tail) == len(pngIEND) && !bytes.Equal(tail, pngIEND) {
			out = append(out, finding(file.Path, lint.SeverityWarning,
				"data appended after the PNG end marker (possible hidden payload)"))
		}
	}

	// Honest text, honest binary, benign ambiguity → nothing (disclosed, not reported).
	return out, nil
}

func finding(path string, sev lint.Severity, msg string) lint.Finding {
	return lint.Finding{File: path, Line: 1, Module: "content", Severity: sev, Message: msg}
}

// readHead returns up to n bytes from the start of a file, or nil when unreadable —
// concealment checks must no-op (not error) on files they can't read.
func readHead(path string, n int) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, n)
	got, _ := io.ReadFull(f, buf)
	return buf[:got]
}

// readTail returns the last n bytes of a file, or nil when unreadable / shorter than n.
func readTail(path string, n int) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() < int64(n) {
		return nil
	}
	if _, err := f.Seek(-int64(n), io.SeekEnd); err != nil {
		return nil
	}
	buf := make([]byte, n)
	got, _ := io.ReadFull(f, buf)
	return buf[:got]
}
