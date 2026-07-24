package config

// BadgeSpec is the reusable internal badge specification.
// Consumed by badge engine, CLI ad-hoc mode, release badge, and docker build badge.
// No YAML tags — this is an internal model, not a config surface.
type BadgeSpec struct {
	Label    string  // left side text (badge name / alt text)
	Value    string  // right side text (supports templates)
	Color    string  // hex color or "auto"
	Output   string  // SVG file output path
	Font     string  // built-in font name override
	FontSize float64 // font size override
	FontFile string  // custom font file override
}
