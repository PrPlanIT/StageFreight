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
// Write() leaves run-time state in THREE places, all derived from the current clock:
//   - head.created / head.modified — the timestamps themselves;
//   - head.checksumAdjustment — a whole-font checksum taken over those timestamps;
//   - the head table's checksum entry in the sfnt table directory — which Write() computed
//     over that run-time checksumAdjustment, so it fossilizes the clock even once the head
//     body is normalized, AND pollutes the whole-font sum that checksumAdjustment is built
//     from (the directory is part of the font).
//
// Zeroing the timestamps, or even the timestamps plus checksumAdjustment, therefore still
// left the directory entry (and hence checksumAdjustment) differing every publish — the
// 2-byte diff that churned a no-op auto-commit onto every run. All three are normalized
// here, in order: timestamps zeroed, checksumAdjustment zeroed, the directory entry
// recomputed over the now-zeroed head (spec-correct: the head checksum is taken with
// checksumAdjustment treated as 0), then checksumAdjustment recomputed over the final
// whole-font bytes — deterministic AND valid. On any structural problem the bytes are
// returned untouched (a churny badge beats a broken one).
func normalizeFontHead(ttf []byte) []byte {
	rec, off, length, ok := headTable(ttf)
	if !ok {
		return ttf
	}
	// Zero head.checksumAdjustment (4 bytes @ +8) and created/modified (16 bytes @ +20).
	binary.BigEndian.PutUint32(ttf[off+8:off+12], 0)
	for j := off + 20; j < off+36; j++ {
		ttf[j] = 0
	}
	// Recompute the head table's directory checksum entry over the now-zeroed head body,
	// so the directory carries no run-time state — this is the byte that churned.
	binary.BigEndian.PutUint32(ttf[rec+4:rec+8], fontChecksum(ttf[off:off+length]))
	// Recompute checksumAdjustment over the final whole-font bytes (its own field already 0):
	// 0xB1B0AFBA − sum. Deterministic now that every byte, directory entry included, is.
	binary.BigEndian.PutUint32(ttf[off+8:off+12], ttfChecksumMagic-fontChecksum(ttf))
	return ttf
}

// headTable locates the head table from the sfnt table directory, returning its directory
// RECORD offset (rec — the 16-byte entry whose +4 field is the table checksum), its table
// body offset (off) and length, and whether a head table large enough to carry
// checksumAdjustment + timestamps (≥36 bytes) was found within bounds.
func headTable(ttf []byte) (rec, off, length int, ok bool) {
	if len(ttf) < 12 {
		return 0, 0, 0, false
	}
	numTables := int(binary.BigEndian.Uint16(ttf[4:6]))
	for i := 0; i < numTables; i++ {
		r := 12 + i*16
		if r+16 > len(ttf) {
			break
		}
		if string(ttf[r:r+4]) == "head" {
			o := int(binary.BigEndian.Uint32(ttf[r+8 : r+12]))
			l := int(binary.BigEndian.Uint32(ttf[r+12 : r+16]))
			if o >= 0 && l >= 36 && o+l <= len(ttf) {
				return r, o, l, true
			}
			return 0, 0, 0, false
		}
	}
	return 0, 0, 0, false
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
