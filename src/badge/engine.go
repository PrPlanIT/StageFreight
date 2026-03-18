package badge

import "fmt"

// Engine generates SVG badges using a specific font.
type Engine struct {
	metrics *FontMetrics
}

// New creates a badge engine with the given font metrics.
func New(metrics *FontMetrics) *Engine {
	return &Engine{metrics: metrics}
}

// NewDefault creates an engine with dejavu-sans 11pt (the standard).
func NewDefault() (*Engine, error) {
	metrics, err := LoadBuiltinFont("dejavu-sans", 11)
	if err != nil {
		return nil, fmt.Errorf("loading badge font: %w", err)
	}
	return New(metrics), nil
}

// NewForSpec creates an engine from font override parameters.
// Falls back to dejavu-sans if no overrides given.
func NewForSpec(font string, fontSize float64, fontFile string) (*Engine, error) {
	if fontSize == 0 {
		fontSize = 11
	}
	var metrics *FontMetrics
	var err error
	switch {
	case fontFile != "":
		metrics, err = LoadFontFile(fontFile, fontSize)
	case font != "":
		metrics, err = LoadBuiltinFont(font, fontSize)
	default:
		metrics, err = LoadBuiltinFont("dejavu-sans", fontSize)
	}
	if err != nil {
		return nil, fmt.Errorf("loading badge font: %w", err)
	}
	return New(metrics), nil
}

// Badge defines the content and appearance of a single badge.
type Badge struct {
	Label string // left side text
	Value string // right side text
	Color string // hex color for right side (e.g. "#4c1")
}

// Generate produces a shields.io-compatible SVG badge string.
func (e *Engine) Generate(b Badge) string {
	return e.renderSVG(b)
}

// StatusColor maps a status keyword to a badge hex color.
func StatusColor(status string) string {
	switch status {
	case "passed", "success":
		return "#4c1"
	case "warning":
		return "#dfb317"
	case "critical", "failed":
		return "#e05d44"
	default:
		return "#4c1"
	}
}
