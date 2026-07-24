package render

import "fmt"

const shieldsBaseURL = "https://img.shields.io/"

// ShieldModule renders a shields.io badge from a path shorthand.
// The path is appended to https://img.shields.io/ to form the image URL.
type ShieldModule struct {
	Path  string // shields.io path (e.g., "docker/pulls/myorg/myrepo")
	Label string // override label (used as alt text; empty = last path segment)
	Link  string // click target URL (empty = no link wrapper)
}

// Render produces the inline markdown for this shields.io badge.
func (s ShieldModule) Render() string {
	alt := s.Label
	if alt == "" {
		alt = lastPathSegment(s.Path)
	}
	imgURL := shieldsBaseURL + s.Path

	if s.Link != "" {
		return fmt.Sprintf("[![%s](%s)](%s)", alt, imgURL, s.Link)
	}
	return fmt.Sprintf("![%s](%s)", alt, imgURL)
}

// lastPathSegment returns the final segment of a slash-delimited path.
func lastPathSegment(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
