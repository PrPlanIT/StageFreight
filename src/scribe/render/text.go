package render

// TextModule renders literal markdown text.
// Template variables ({version}, {env:VAR}, etc.) are resolved before rendering.
type TextModule struct {
	Text string // markdown content (templates already resolved by caller)
}

// Render returns the text content as-is.
func (t TextModule) Render() string {
	return t.Text
}
