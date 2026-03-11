package props

import "fmt"

// Variant controls the visual style of rendered output.
// v1 implements classic only. Other values are reserved and rejected at validation.
type Variant string

const (
	// VariantClassic is the standard markdown badge — v1 default and only implementation.
	VariantClassic Variant = "classic"
)

// ValidateVariant returns an error if the variant is not recognized.
// Empty string is accepted as an alias for VariantClassic.
func ValidateVariant(v Variant) error {
	switch v {
	case "", VariantClassic:
		return nil
	default:
		return fmt.Errorf("unknown variant %q (supported: classic)", v)
	}
}

// FormatMarkdown formats a resolved prop as inline markdown.
// Used by both narrator PropsModule and `props render` CLI.
// Returns empty string if ImageURL is empty.
// Panics on unknown variant — callers must validate via ValidateVariant first.
func FormatMarkdown(p ResolvedProp, variant Variant) string {
	if p.ImageURL == "" {
		return ""
	}

	alt := p.Alt
	if alt == "" {
		alt = "badge"
	}

	// v1: classic is the only implementation. The switch is the seam for future variants.
	switch variant {
	case "", VariantClassic:
		if p.LinkURL != "" {
			return fmt.Sprintf("[![%s](%s)](%s)", alt, p.ImageURL, p.LinkURL)
		}
		return fmt.Sprintf("![%s](%s)", alt, p.ImageURL)
	default:
		// Should not reach here if ValidateVariant was called.
		return fmt.Sprintf("![%s](%s)", alt, p.ImageURL)
	}
}
