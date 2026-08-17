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
	return normalizeFontHead(sub.Write()), nil
}

// ttfChecksumMagic is the constant the TrueType head.checksumAdjustment is derived from:
// checksumAdjustment = 0xB1B0AFBA − (whole-font checksum with this field treated as zero).
const ttfChecksumMagic uint32 = 0xB1B0AFBA

// normalizeFontHead makes the subset font byte-REPRODUCIBLE run to run. tdewolff/font
// Write() leaves the head table carrying run-time state in TWO fields: the created/modified
// timestamps (stamped with the current time) and checksumAdjustment (a whole-font checksum
// computed over those timestamps — so it FOSSILIZES the run-time value even once the
// timestamps themselves are zeroed). Zeroing the timestamps alone therefore still left
// checksumAdjustment differing every publish, which is what churned a no-op commit onto
// every run. Both are normalized here: the timestamps are zeroed, then checksumAdjustment
// is recomputed over the final bytes, so it is deterministic AND valid. On any structural
// problem the bytes are returned untouched (a churny badge beats a broken one).
func normalizeFontHead(ttf []byte) []byte {
	headOff, ok := headTableOffset(ttf)
	if !ok {
		return ttf
	}
	// head layout: … created (8 bytes @ +20), modified (8 bytes @ +28) — zero both.
	if headOff+36 <= len(ttf) {
		for j := headOff + 20; j < headOff+36; j++ {
			ttf[j] = 0
		}
	}
	// checksumAdjustment (4 bytes @ +8): zero it (per spec it counts as zero while the
	// checksum is taken), sum the whole font, then write back 0xB1B0AFBA − sum.
	if headOff+12 <= len(ttf) {
		binary.BigEndian.PutUint32(ttf[headOff+8:headOff+12], 0)
		binary.BigEndian.PutUint32(ttf[headOff+8:headOff+12], ttfChecksumMagic-fontChecksum(ttf))
	}
	return ttf
}

// headTableOffset returns the byte offset of the head table from the sfnt table directory,
// and whether a head table large enough to carry checksumAdjustment + timestamps was found.
func headTableOffset(ttf []byte) (int, bool) {
	if len(ttf) < 12 {
		return 0, false
	}
	numTables := int(binary.BigEndian.Uint16(ttf[4:6]))
	for i := 0; i < numTables; i++ {
		rec := 12 + i*16
		if rec+16 > len(ttf) {
			break
		}
		if string(ttf[rec:rec+4]) == "head" {
			off := int(binary.BigEndian.Uint32(ttf[rec+8 : rec+12]))
			if off >= 0 && off+36 <= len(ttf) {
				return off, true
			}
			return 0, false
		}
	}
	return 0, false
}

// fontChecksum is the TrueType table checksum over the whole font: the sum of its
// big-endian uint32 words (a trailing partial word zero-padded), with uint32 overflow
// wrapping. Used to derive head.checksumAdjustment.
func fontChecksum(ttf []byte) uint32 {
	var sum uint32
	i := 0
	for ; i+4 <= len(ttf); i += 4 {
		sum += binary.BigEndian.Uint32(ttf[i : i+4])
	}
	if i < len(ttf) {
		var last [4]byte
		copy(last[:], ttf[i:])
		sum += binary.BigEndian.Uint32(last[:])
	}
	return sum
}
