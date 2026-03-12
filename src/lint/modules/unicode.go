package modules

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("unicode", func() lint.Module {
		return &unicodeModule{cfg: defaultUnicodeConfig()}
	})
}

// --------------------------------------------------------------------
// Configuration
// --------------------------------------------------------------------

// unicodeConfig holds per-category toggles and the ASCII control char allowlist.
// Parsed from module options via JSON round-trip (same pattern as freshness).
type unicodeConfig struct {
	DetectBidi         bool     `json:"detect_bidi"`
	DetectZeroWidth    bool     `json:"detect_zero_width"`
	DetectControlASCII bool     `json:"detect_control_ascii"`
	AllowControlPaths  []string `json:"allow_control_ascii_in_paths"`
	AllowControlBytes  []int    `json:"allow_control_ascii"`
}

func defaultUnicodeConfig() unicodeConfig {
	return unicodeConfig{
		DetectBidi:         true,
		DetectZeroWidth:    true,
		DetectControlASCII: true,
	}
}

// --------------------------------------------------------------------
// Category constants
// --------------------------------------------------------------------

const (
	catOther = iota
	catBidi
	catZeroWidth
	catControlASCII
)

// runeCategory mirrors the same rune ranges as checkRune / severityForRune.
func runeCategory(r rune) int {
	switch r {
	case '\u202A', '\u202B', '\u202C', '\u202D', '\u202E',
		'\u2066', '\u2067', '\u2068', '\u2069':
		return catBidi
	case '\u200B', '\u200C', '\u200D', '\uFEFF':
		return catZeroWidth
	}
	if r < 0x80 && unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
		return catControlASCII
	}
	return catOther
}

// --------------------------------------------------------------------
// Module
// --------------------------------------------------------------------

type unicodeModule struct {
	cfg            unicodeConfig
	allowedControl map[byte]bool // built from AllowControlBytes during Configure
}

func (m *unicodeModule) Name() string        { return "unicode" }
func (m *unicodeModule) DefaultEnabled() bool { return true }
func (m *unicodeModule) AutoDetect() []string { return nil }

// Configure implements lint.ConfigurableModule.
func (m *unicodeModule) Configure(opts map[string]any) error {
	cfg := defaultUnicodeConfig()
	if len(opts) != 0 {
		b, err := json.Marshal(opts)
		if err != nil {
			return fmt.Errorf("unicode: marshal options: %w", err)
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("unicode: unmarshal options: %w", err)
		}
	}

	allowed := make(map[byte]bool)
	for _, v := range cfg.AllowControlBytes {
		if v < 0 || v > 31 {
			return fmt.Errorf("unicode: allow_control_ascii contains %d (must be 0..31)", v)
		}
		if v == 9 || v == 10 || v == 13 {
			return fmt.Errorf("unicode: allow_control_ascii contains %d (tab/newline/carriage return are always allowed; do not list them)", v)
		}
		allowed[byte(v)] = true // de-dupe via map
	}

	// Normalize allowlist patterns to forward slashes once.
	for i := range cfg.AllowControlPaths {
		cfg.AllowControlPaths[i] = normalizeSlashPath(cfg.AllowControlPaths[i])
	}

	m.cfg = cfg
	m.allowedControl = allowed
	return nil
}

// Check scans a file for invisible, confusable, and dangerous Unicode characters.
func (m *unicodeModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	f, err := os.Open(file.AbsPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var findings []lint.Finding
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()

		if !utf8.Valid(line) {
			findings = append(findings, lint.Finding{
				File:     file.Path,
				Line:     lineNum,
				Module:   m.Name(),
				Severity: lint.SeverityWarning,
				Message:  "invalid UTF-8 encoding",
			})
			continue
		}

		col := 0
		for i := 0; i < len(line); {
			r, size := utf8.DecodeRune(line[i:])
			col++

			msg := checkRune(r)
			if msg == "" {
				i += size
				continue
			}

			// Category-aware gating — only intercepts the three
			// configurable categories; everything else (confusables,
			// tag chars, etc.) always fires.
			cat := runeCategory(r)
			switch cat {
			case catBidi:
				if !m.cfg.DetectBidi {
					i += size
					continue
				}
			case catZeroWidth:
				if !m.cfg.DetectZeroWidth {
					i += size
					continue
				}
			case catControlASCII:
				if !m.cfg.DetectControlASCII {
					i += size
					continue
				}
				if m.isAllowedControl(file.Path, r) {
					i += size
					continue
				}
			}

			findings = append(findings, lint.Finding{
				File:     file.Path,
				Line:     lineNum,
				Column:   col,
				Module:   m.Name(),
				Severity: severityForRune(r),
				Message:  fmt.Sprintf("%s (U+%04X)", msg, r),
			})

			i += size
		}
	}

	return findings, scanner.Err()
}

// isAllowedControl checks whether a specific ASCII control byte is explicitly
// allowed in a given file path via the module configuration.
func (m *unicodeModule) isAllowedControl(path string, r rune) bool {
	if r < 0 || r >= 0x80 {
		return false
	}
	if !m.allowedControl[byte(r)] {
		return false
	}
	normPath := normalizeSlashPath(path)
	for _, pat := range m.cfg.AllowControlPaths {
		if lint.MatchGlob(pat, normPath) {
			return true
		}
	}
	return false
}

// normalizeSlashPath converts a path to forward slashes and strips leading "./".
func normalizeSlashPath(p string) string {
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	return p
}

// --------------------------------------------------------------------
// Rune classification (unchanged)
// --------------------------------------------------------------------

func checkRune(r rune) string {
	// Bidi override characters — can change text rendering direction for attacks
	switch r {
	case '\u202A': // LEFT-TO-RIGHT EMBEDDING
		return "bidi override: left-to-right embedding"
	case '\u202B': // RIGHT-TO-LEFT EMBEDDING
		return "bidi override: right-to-left embedding"
	case '\u202C': // POP DIRECTIONAL FORMATTING
		return "bidi override: pop directional formatting"
	case '\u202D': // LEFT-TO-RIGHT OVERRIDE
		return "bidi override: left-to-right override"
	case '\u202E': // RIGHT-TO-LEFT OVERRIDE
		return "bidi override: right-to-left override"
	case '\u2066': // LEFT-TO-RIGHT ISOLATE
		return "bidi override: left-to-right isolate"
	case '\u2067': // RIGHT-TO-LEFT ISOLATE
		return "bidi override: right-to-left isolate"
	case '\u2068': // FIRST STRONG ISOLATE
		return "bidi override: first strong isolate"
	case '\u2069': // POP DIRECTIONAL ISOLATE
		return "bidi override: pop directional isolate"
	}

	// Zero-width characters — invisible, used for trojan source attacks
	switch r {
	case '\u200B': // ZERO WIDTH SPACE
		return "zero-width space"
	case '\u200C': // ZERO WIDTH NON-JOINER
		return "zero-width non-joiner"
	case '\u200D': // ZERO WIDTH JOINER
		return "zero-width joiner"
	case '\uFEFF': // ZERO WIDTH NO-BREAK SPACE (BOM mid-file)
		return "zero-width no-break space (unexpected BOM)"
	}

	// Other invisible/confusable characters
	switch r {
	case '\u00AD': // SOFT HYPHEN
		return "soft hyphen (invisible)"
	case '\u034F': // COMBINING GRAPHEME JOINER
		return "combining grapheme joiner"
	case '\u2060': // WORD JOINER
		return "word joiner (invisible)"
	case '\u2061', '\u2062', '\u2063', '\u2064': // FUNCTION APPLICATION, INVISIBLE TIMES/SEPARATOR/PLUS
		return "invisible math operator"
	case '\u180E': // MONGOLIAN VOWEL SEPARATOR
		return "mongolian vowel separator (invisible whitespace)"
	}

	// Confusable whitespace (looks like a space but isn't)
	switch r {
	case '\u00A0': // NO-BREAK SPACE
		return "non-breaking space"
	case '\u2000', '\u2001', '\u2002', '\u2003', '\u2004',
		'\u2005', '\u2006', '\u2007', '\u2008', '\u2009', '\u200A':
		return "unusual whitespace character"
	case '\u205F': // MEDIUM MATHEMATICAL SPACE
		return "medium mathematical space"
	case '\u3000': // IDEOGRAPHIC SPACE
		return "ideographic space"
	}

	// Control characters (except normal whitespace: \t \n \r)
	if r != '\t' && r != '\n' && r != '\r' && unicode.IsControl(r) && r < 0x80 {
		return "ASCII control character"
	}

	// Tag characters (U+E0001-U+E007F) — used for language tagging, exploitable
	if r >= 0xE0001 && r <= 0xE007F {
		return "tag character (invisible)"
	}

	return ""
}

func severityForRune(r rune) lint.Severity {
	// Bidi overrides and zero-width chars are critical — trojan source attack vectors
	switch {
	case r >= '\u202A' && r <= '\u202E':
		return lint.SeverityCritical
	case r >= '\u2066' && r <= '\u2069':
		return lint.SeverityCritical
	case r == '\u200B' || r == '\u200C' || r == '\u200D' || r == '\uFEFF':
		return lint.SeverityCritical
	case r >= 0xE0001 && r <= 0xE007F:
		return lint.SeverityCritical
	default:
		return lint.SeverityWarning
	}
}
