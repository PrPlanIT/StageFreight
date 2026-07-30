package badge

import (
	"encoding/binary"

	tdfont "github.com/tdewolff/font"
)

// badgeCharset is the realistic set of characters a badge value can physically
// carry — printable ASCII plus a curated handful of symbols/units. The embedded
// font is subset to exactly these glyphs, which takes each badge SVG from ~MB
// (whole font) to ~KB while keeping real, selectable, EDITABLE <text> in the
// exact custom font. A character outside this set still renders: the SVG's
// font-family fallback (Verdana/sans-serif) draws it in a system font, so the
// charset only has to cover the common case — the fallback catches the rest.
func badgeCharset() []rune {
	rs := make([]rune, 0, 128)
	for r := rune(0x20); r <= 0x7E; r++ { // printable ASCII — covers dates, sizes, versions, counts, statuses, tags
		rs = append(rs, r)
	}
	// symbols/units a value realistically uses (sizes, math, arrows, currency, marks)
	rs = append(rs, '×', '÷', '•', '·', '–', '—', '…', '±', '≈', '→', '←', '↑', '↓',
		'µ', '°', '©', '®', '™', '€', '£', '¥', '§', '✓', '✗')
	return rs
}

// subsetToCharset trims a TTF/OTF down to badgeCharset via tdewolff/font, keeping
// the minimal renderable table set that INCLUDES cmap — so a browser can still
// map the <text> characters to glyphs (KeepPDFTables drops cmap and would render
// blank; KeepAllTables keeps GSUB/GPOS/hinting bloat we don't need). It is font-
// agnostic: identical for a built-in font and a user-supplied FontFile, which is
// why this is code, not a vendored per-font subset. On ANY failure it returns an
// error and the caller keeps the full font — this never yields a broken font.
func subsetToCharset(data []byte) ([]byte, error) {
	sf, err := tdfont.ParseSFNT(data, 0)
	if err != nil {
		return nil, err
	}
	glyphIDs := []uint16{0} // .notdef must lead; composite-glyph deps are added automatically
	seen := map[uint16]bool{0: true}
	for _, r := range badgeCharset() {
		gid := sf.GlyphIndex(r)
		if gid == 0 || seen[gid] { // rune absent in this font (→ .notdef), or already added
			continue
		}
		seen[gid] = true
		glyphIDs = append(glyphIDs, gid)
	}
	sub, err := sf.Subset(glyphIDs, tdfont.SubsetOptions{Tables: tdfont.KeepMinTables})
	if err != nil {
		return nil, err
	}
	return zeroFontTimestamps(sub.Write()), nil
}

// zeroFontTimestamps zeros the head table's created/modified timestamps so the
// subset font is byte-REPRODUCIBLE run to run. tdewolff/font stamps them with the
// current time on Write, which otherwise makes every badge SVG differ every run —
// churning a no-op commit onto every publish. The head checksum goes stale, but SVG
// @font-face renderers don't validate it. On any parse failure it returns the bytes
// untouched (a churny badge beats a broken one).
func zeroFontTimestamps(ttf []byte) []byte {
	if len(ttf) < 12 {
		return ttf
	}
	numTables := int(binary.BigEndian.Uint16(ttf[4:6]))
	for i := 0; i < numTables; i++ {
		rec := 12 + i*16
		if rec+16 > len(ttf) {
			break
		}
		if string(ttf[rec:rec+4]) == "head" {
			off := int(binary.BigEndian.Uint32(ttf[rec+8 : rec+12]))
			// head layout: … created (8 bytes @ +20), modified (8 bytes @ +28).
			if off >= 0 && off+36 <= len(ttf) {
				for j := off + 20; j < off+36; j++ {
					ttf[j] = 0
				}
			}
			break
		}
	}
	return ttf
}
