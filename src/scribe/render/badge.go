package render

import "fmt"

// BadgeModule renders a markdown badge image, optionally wrapped in a link.
type BadgeModule struct {
	Alt    string // image alt text
	ImgURL string // resolved image URL
	Link   string // resolved click target (empty = no link wrapper)
}

// Render produces the inline markdown for this badge.
func (b BadgeModule) Render() string {
	if b.Link != "" {
		return fmt.Sprintf("[![%s](%s)](%s)", b.Alt, b.ImgURL, b.Link)
	}
	return fmt.Sprintf("![%s](%s)", b.Alt, b.ImgURL)
}
