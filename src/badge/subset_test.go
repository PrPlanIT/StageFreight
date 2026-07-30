package badge

import (
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
