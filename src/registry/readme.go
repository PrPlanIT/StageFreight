package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ReadmeContent holds the processed README ready for pushing to registries.
type ReadmeContent struct {
	Short string // max 100 chars
	Full  string // full markdown
}

// PrepareReadmeFromFile loads a README file and returns processed content ready for registry sync.
// linkBase is the blob/page URL base for clickable links.
// rawBase is the raw content URL base for embedded images.
// Both are resolved from publish-origin repo role by the caller.
func PrepareReadmeFromFile(file, description, linkBase, rawBase, rootDir string) (*ReadmeContent, error) {
	if file == "" {
		file = "README.md"
	}

	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(rootDir, path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("readme: reading %s: %w", file, err)
	}
	content := string(raw)

	// Rewrite relative links and images to absolute URLs.
	if linkBase != "" || rawBase != "" {
		content = rewriteRelativeLinks(content, linkBase, rawBase)
	}

	// Generate short description
	short := description
	if short == "" {
		short = extractFirstParagraph(content)
	}
	short = truncateAtWord(short, 100)

	return &ReadmeContent{
		Short: short,
		Full:  content,
	}, nil
}

// extractMarkers pulls content between start and end markers (exclusive of the markers themselves).
func extractMarkers(content, start, end string) (string, error) {
	startIdx := strings.Index(content, start)
	if startIdx < 0 {
		return "", fmt.Errorf("readme: start marker %q not found", start)
	}
	startIdx += len(start)

	endIdx := strings.Index(content[startIdx:], end)
	if endIdx < 0 {
		return "", fmt.Errorf("readme: end marker %q not found", end)
	}

	return strings.TrimSpace(content[startIdx : startIdx+endIdx]), nil
}

// rewriteRelativeLinks rewrites relative links and image sources to absolute URLs.
// Uses linkBase for clickable links (markdown [text](path), HTML href) and
// rawBase (derived from linkBase) for embeddable assets (markdown ![alt](path), HTML img src).
//
// Skips: http://, https://, mailto:, data:, #anchor, protocol-relative //.
var (
	mdImagePattern  = regexp.MustCompile(`(!\[[^\]]*\]\()([^)]+)(\))`)
	mdLinkPattern   = regexp.MustCompile(`(\[[^\]]+\]\()([^)]+)(\))`)
	htmlImgSrcPattern = regexp.MustCompile(`(?i)(<img\b[^>]*\bsrc=["'])([^"']+)(["'][^>]*>)`)
	htmlHrefPattern   = regexp.MustCompile(`(?i)(\bhref=["'])([^"']+)(["'])`)
)

func rewriteRelativeLinks(content, linkBase, rawBase string) string {
	linkBase = strings.TrimRight(linkBase, "/")
	rawBase = strings.TrimRight(rawBase, "/")

	isRelative := func(s string) bool {
		if s == "" {
			return false
		}
		l := strings.ToLower(s)
		return !strings.HasPrefix(l, "http://") &&
			!strings.HasPrefix(l, "https://") &&
			!strings.HasPrefix(l, "mailto:") &&
			!strings.HasPrefix(l, "data:") &&
			!strings.HasPrefix(s, "#") &&
			!strings.HasPrefix(s, "//") &&
			!strings.HasPrefix(s, "/")
	}

	join := func(base, rel string) string {
		return base + "/" + strings.TrimPrefix(rel, "./")
	}

	// Markdown images: ![alt](path) → rawBase
	if rawBase != "" {
		content = mdImagePattern.ReplaceAllStringFunc(content, func(m string) string {
			sub := mdImagePattern.FindStringSubmatch(m)
			if len(sub) != 4 || !isRelative(sub[2]) {
				return m
			}
			return sub[1] + join(rawBase, sub[2]) + sub[3]
		})
	}

	// Markdown links: [text](path) → linkBase
	content = mdLinkPattern.ReplaceAllStringFunc(content, func(m string) string {
		sub := mdLinkPattern.FindStringSubmatch(m)
		if len(sub) != 4 || !isRelative(sub[2]) {
			return m
		}
		return sub[1] + join(linkBase, sub[2]) + sub[3]
	})

	// HTML img src="..." → rawBase
	if rawBase != "" {
		content = htmlImgSrcPattern.ReplaceAllStringFunc(content, func(m string) string {
			sub := htmlImgSrcPattern.FindStringSubmatch(m)
			if len(sub) != 4 || !isRelative(sub[2]) {
				return m
			}
			return sub[1] + join(rawBase, sub[2]) + sub[3]
		})
	}

	// HTML href="..." → linkBase
	content = htmlHrefPattern.ReplaceAllStringFunc(content, func(m string) string {
		sub := htmlHrefPattern.FindStringSubmatch(m)
		if len(sub) != 4 || !isRelative(sub[2]) {
			return m
		}
		return sub[1] + join(linkBase, sub[2]) + sub[3]
	})

	return content
}

// extractFirstParagraph returns the first prose paragraph from markdown content.
// Skips headings, badges (lines starting with [! or [![), HTML comments, and blank lines.
func extractFirstParagraph(content string) string {
	lines := strings.Split(content, "\n")
	var para []string
	inComment := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track HTML comments
		if strings.Contains(trimmed, "<!--") {
			inComment = true
		}
		if strings.Contains(trimmed, "-->") {
			inComment = false
			continue
		}
		if inComment {
			continue
		}

		// Skip blank lines (but end paragraph collection if we have content)
		if trimmed == "" {
			if len(para) > 0 {
				break
			}
			continue
		}

		// Skip headings
		if strings.HasPrefix(trimmed, "#") {
			if len(para) > 0 {
				break
			}
			continue
		}

		// Skip badge lines
		if strings.HasPrefix(trimmed, "[![") || strings.HasPrefix(trimmed, "[!") {
			continue
		}

		// Skip HTML tags
		if strings.HasPrefix(trimmed, "<") && !strings.HasPrefix(trimmed, "<a") {
			continue
		}

		para = append(para, trimmed)
	}

	return strings.Join(para, " ")
}
