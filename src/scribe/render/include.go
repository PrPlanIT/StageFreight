package render

// IncludeModule renders verbatim file content included via kind: include.
// The file is read by the caller; this module carries the raw content
// into the scribe composition pipeline without transformation.
type IncludeModule struct {
	Content string // raw file content (read by caller)
}

// Render returns the included content as-is.
func (i IncludeModule) Render() string {
	return i.Content
}
