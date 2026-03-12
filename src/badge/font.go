// Package badge provides a configurable SVG badge engine with dynamic font measurement.
package badge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/fonts"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// FontMetrics holds measured glyph widths and font data for SVG embedding.
type FontMetrics struct {
	name     string           // font family name
	size     float64          // point size
	data     []byte           // raw TTF/OTF bytes for base64 embedding
	advances map[rune]float64 // measured glyph advances (printable ASCII)
	fallback float64          // average width for unmapped runes
}

// TextWidth returns the pixel width of s using measured glyph advances.
func (m *FontMetrics) TextWidth(s string) float64 {
	var w float64
	for _, r := range s {
		if adv, ok := m.advances[r]; ok {
			w += adv
		} else {
			w += m.fallback
		}
	}
	return w
}

// FontData returns the raw font bytes for SVG embedding.
func (m *FontMetrics) FontData() []byte { return m.data }

// FontName returns the font family name.
func (m *FontMetrics) FontName() string { return m.name }

// FontSize returns the configured point size.
func (m *FontMetrics) FontSize() float64 { return m.size }

// LoadFont loads a TTF/OTF from raw bytes and measures glyph advances at the given size.
// This is the single code path for ALL fonts — built-in and custom alike.
func LoadFont(name string, data []byte, size float64) (*FontMetrics, error) {
	f, err := sfnt.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parsing font %s: %w", name, err)
	}

	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size: size,
		DPI:  72,
	})
	if err != nil {
		return nil, fmt.Errorf("creating face for %s: %w", name, err)
	}
	defer face.Close()

	advances := make(map[rune]float64, 95)
	var total float64
	var count int

	for r := rune(32); r <= 126; r++ {
		adv, ok := face.GlyphAdvance(r)
		if !ok {
			continue
		}
		px := float64(adv) / 64.0 // fixed.Int26_6 → float64
		advances[r] = px
		total += px
		count++
	}

	var fallback float64
	if count > 0 {
		fallback = total / float64(count)
	} else {
		fallback = size * 0.6 // reasonable estimate
	}

	// Try to extract font family name from the name table.
	familyName := name
	buf := &sfnt.Buffer{}
	if n, err := f.Name(buf, sfnt.NameIDFamily); err == nil && n != "" {
		familyName = n
	}

	return &FontMetrics{
		name:     familyName,
		size:     size,
		data:     data,
		advances: advances,
		fallback: fallback,
	}, nil
}

// LoadBuiltinFont loads an embedded font by config name.
func LoadBuiltinFont(name string, size float64) (*FontMetrics, error) {
	filename, ok := fonts.Builtin[name]
	if !ok {
		return nil, fmt.Errorf("unknown built-in font %q (available: %v)", name, fonts.Names())
	}
	data, err := fonts.FS.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("reading embedded font %s: %w", name, err)
	}
	return LoadFont(name, data, size)
}

// LoadFontFile loads a TTF/OTF from a filesystem path.
func LoadFontFile(path string, size float64) (*FontMetrics, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading font file %s: %w", path, err)
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return LoadFont(name, data, size)
}

// fontFace is unexported — ensures face.Close() via font.Face interface check.
var _ font.Face = (*opentype.Face)(nil)
