package gitver

import (
	"regexp"
	"strings"
)

// Conventional is a commit message read as a Conventional Commit (conventionalcommits.org 1.0).
type Conventional struct {
	Type        string // lower-cased; empty when the subject is not conventional
	Scope       string
	Description string // the subject after "type(scope)!: "; the whole subject when not conventional
	Body        string
	Breaking    bool // "!" before the colon, or a BREAKING CHANGE / BREAKING-CHANGE footer
}

var (
	conventionalSubjectRe = regexp.MustCompile(`^(\w+)(?:\(([^)]+)\))?(!)?\s*:\s*(.+)`)
	// The spec's breaking footer: the uppercase token at the start of a line, then ": " or " #".
	breakingFooterRe = regexp.MustCompile(`(?m)^BREAKING[ -]CHANGE(?:: | #)`)
)

// ParseConventional reads a full commit message.
func ParseConventional(message string) Conventional {
	message = strings.TrimSpace(message)
	subject, body, _ := strings.Cut(message, "\n")
	c := Conventional{Description: strings.TrimSpace(subject), Body: strings.TrimSpace(body)}
	if m := conventionalSubjectRe.FindStringSubmatch(c.Description); m != nil {
		c.Type = strings.ToLower(m[1])
		c.Scope = m[2]
		c.Breaking = m[3] == "!"
		c.Description = m[4]
	}
	if breakingFooterRe.MatchString(c.Body) {
		c.Breaking = true
	}
	return c
}
