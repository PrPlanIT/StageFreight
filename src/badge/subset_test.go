package badge

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	tdfont "github.com/tdewolff/font"
)

// The embedded font must shrink from the full ~757KB DejaVu to a small subset,
// while staying a real, parseable font with a working cmap (so <text> still maps
// characters to glyphs in a browser).
func TestSubset_ShrinksFontButKeepsCmap(t *testing.T) {
	m, err := LoadBuiltinFont("dejavu-sans", 11)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("dejavu-sans embedded font: %d bytes (full is ~757000)", len(m.FontData()))
	if n := len(m.FontData()); n > 200_000 {
		t.Fatalf("subset font is %d bytes; expected a small fraction of the 757KB full font", n)
	}
	sf, err := tdfont.ParseSFNT(m.FontData(), 0)
	if err != nil {
		t.Fatalf("subset font does not parse: %v", err)
	}
	if sf.GlyphIndex('A') == 0 || sf.GlyphIndex('2') == 0 || sf.GlyphIndex('M') == 0 {
		t.Fatal("subset cmap lost common badge glyphs — <text> would render blank")
	}
}

// The subset font must be byte-reproducible across runs — i.e. the head table's
// created/modified timestamps are zeroed. (A "subset twice, compare bytes" test would
// flake: LONGDATETIME is second-resolution, so two rapid in-process subsets land in the
// same second and match even when the bug is present. Asserting the zeroed timestamp is
// the robust check.) A stale head timestamp made every badge SVG differ every publish,
// churning a no-op commit.
func TestSubset_HeadTimestampsZeroed(t *testing.T) {
	m, err := LoadBuiltinFont("monofur", 11)
	if err != nil {
		t.Fatal(err)
	}
	ttf := m.FontData()
	if len(ttf) < 12 {
		t.Fatalf("subset font too small: %d bytes", len(ttf))
	}
	numTables := int(binary.BigEndian.Uint16(ttf[4:6]))
	headOff := -1
	for i := 0; i < numTables; i++ {
		rec := 12 + i*16
		if rec+16 > len(ttf) {
			break
		}
		if string(ttf[rec:rec+4]) == "head" {
			headOff = int(binary.BigEndian.Uint32(ttf[rec+8 : rec+12]))
			break
		}
	}
	if headOff < 0 {
		t.Fatal("no head table in subset font")
	}
	for j := headOff + 20; j < headOff+36; j++ {
		if ttf[j] != 0 {
			t.Fatalf("head created/modified not zeroed at byte %d — subset font is NOT reproducible run-to-run", j)
		}
	}
}

// The subset font must be byte-identical regardless of the run-time state tdewolff stamps
// at Write time. Zeroing created/modified alone was not enough, nor even zeroing them plus
// head.checksumAdjustment: tdewolff ALSO writes the head table's checksum entry in the sfnt
// directory over the run-time checksumAdjustment, so that entry fossilized the clock and
// pulled checksumAdjustment along with it (the 2-byte diff that churned a no-op auto-commit).
// Perturbing ALL THREE — timestamps, checksumAdjustment, AND the directory entry — as a
// later publish's clock would, then re-normalizing, must reproduce the same bytes, and the
// checksumAdjustment written must be valid.
func TestNormalizeFontHead_TimestampIndependentAndValid(t *testing.T) {
	m, err := LoadBuiltinFont("monofur", 11)
	if err != nil {
		t.Fatal(err)
	}
	base := m.FontData() // already normalized by subsetToCharset
	rec, headOff, _, ok := headTable(base)
	if !ok {
		t.Fatal("no head table in subset font")
	}

	// Simulate a different publish: non-zero created/modified, the bogus checksumAdjustment
	// tdewolff would leave under a different clock, and the stale directory checksum entry
	// it derives from that run-time checksumAdjustment.
	perturbed := append([]byte(nil), base...)
	for j := headOff + 20; j < headOff+36; j++ {
		perturbed[j] = 0x7F
	}
	binary.BigEndian.PutUint32(perturbed[headOff+8:headOff+12], 0xDEADBEEF)
	binary.BigEndian.PutUint32(perturbed[rec+4:rec+8], 0xCAFEF00D)

	got := normalizeFontHead(perturbed)
	if !bytes.Equal(got, base) {
		t.Fatal("normalizeFontHead is not run-time-independent — badge SVGs will still churn a no-op commit")
	}

	// The directory checksum entry it wrote must be the spec-correct head checksum, taken
	// with checksumAdjustment treated as 0 (not over the final body, which now carries the
	// real checksumAdjustment) — the deterministic byte that stops the churn.
	_, off, length, _ := headTable(got)
	headCAdjZeroed := append([]byte(nil), got[off:off+length]...)
	binary.BigEndian.PutUint32(headCAdjZeroed[8:12], 0)
	if entry := binary.BigEndian.Uint32(got[rec+4 : rec+8]); entry != fontChecksum(headCAdjZeroed) {
		t.Errorf("head directory checksum entry not the spec-correct (cadj=0) value: got %08x", entry)
	}

	// The checksumAdjustment it wrote must be VALID: zero the field, recompute, match.
	adj := binary.BigEndian.Uint32(got[headOff+8 : headOff+12])
	zeroed := append([]byte(nil), got...)
	binary.BigEndian.PutUint32(zeroed[headOff+8:headOff+12], 0)
	if want := ttfChecksumMagic - fontChecksum(zeroed); want != adj {
		t.Errorf("checksumAdjustment invalid: got %08x want %08x", adj, want)
	}
}

// A rendered badge must be small AND accessible (real <text> + aria/title).
func TestRender_SmallAndAccessible(t *testing.T) {
	e, err := NewForSpec("dejavu-sans", 11, "")
	if err != nil {
		t.Fatal(err)
	}
	svg := e.Generate(Badge{Label: "size", Value: "82.2 MB", Color: "#555"})
	t.Logf("rendered badge SVG: %d bytes (was ~1010000)", len(svg))
	if len(svg) > 150_000 {
		t.Fatalf("badge SVG is %d bytes; the whole point was to shrink it from ~1MB", len(svg))
	}
	for _, want := range []string{
		`role="img"`,
		`aria-label="size: 82.2 MB"`,
		`<title>size: 82.2 MB</title>`,
		`>82.2 MB<`, // the value is still real, selectable <text>
	} {
		if !strings.Contains(svg, want) {
			t.Fatalf("badge SVG missing %q:\n%.400s", want, svg)
		}
	}
}

// An out-of-charset character (emoji) must not break generation — it just falls
// back to the SVG font-family (system font) for that glyph.
func TestRender_OutOfCharsetStillValid(t *testing.T) {
	e, err := NewForSpec("dejavu-sans", 11, "")
	if err != nil {
		t.Fatal(err)
	}
	svg := e.Generate(Badge{Label: "x", Value: "🚀 go", Color: "#555"})
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatal("out-of-charset value broke SVG generation")
	}
}
