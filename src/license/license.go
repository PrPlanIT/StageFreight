// Package license names a project's license from its license file.
package license

import "strings"

// Files are the license file names, in the order they are read.
var Files = []string{
	"LICENSE", "LICENSE.md", "LICENSE.txt",
	"LICENCE", "LICENCE.md", "LICENCE.txt",
	"COPYING", "COPYING.md",
}

// Detect returns the SPDX identifier of the first license file read reports, or "".
func Detect(read func(name string) ([]byte, error)) string {
	for _, name := range Files {
		data, err := read(name)
		if err != nil {
			continue
		}
		if id := Match(string(data)); id != "" {
			return id
		}
	}
	return ""
}

// Match identifies an SPDX identifier from license text content.
func Match(text string) string {
	lower := strings.ToLower(text)

	// A GNU license is named by its title and version lines: every GPL text mentions the
	// others in its body, and a notice ("version 2, or any later version") names no single
	// license.
	if h := headLines(text, 2); len(h) == 2 {
		title := strings.Join(strings.Fields(strings.ToUpper(h[0])), " ")
		version := strings.Fields(strings.ToUpper(h[1]))
		if len(version) >= 2 && version[0] == "VERSION" {
			v := strings.TrimSuffix(version[1], ",")
			switch {
			case title == "GNU AFFERO GENERAL PUBLIC LICENSE" && v == "3":
				return "AGPL-3.0"
			case title == "GNU LESSER GENERAL PUBLIC LICENSE" && v == "3":
				return "LGPL-3.0"
			case title == "GNU LESSER GENERAL PUBLIC LICENSE" && v == "2.1":
				return "LGPL-2.1"
			case title == "GNU GENERAL PUBLIC LICENSE" && v == "3":
				return "GPL-3.0"
			case title == "GNU GENERAL PUBLIC LICENSE" && v == "2":
				return "GPL-2.0"
			}
		}
	}

	switch {
	case strings.Contains(lower, "apache license") && strings.Contains(lower, "version 2.0"):
		return "Apache-2.0"
	case strings.Contains(strings.Join(strings.Fields(lower), " "), "permission is hereby granted, free of charge, to any person obtaining a copy"):
		return "MIT"
	case strings.Contains(lower, "redistribution and use") && strings.Contains(lower, "all advertising materials"):
		return "BSD-4-Clause"
	case strings.Contains(lower, "bsd 3-clause"),
		strings.Contains(lower, "redistribution and use") && strings.Contains(lower, "neither the name"):
		return "BSD-3-Clause"
	case strings.Contains(lower, "bsd 2-clause"),
		strings.Contains(lower, "redistribution and use") && !strings.Contains(lower, "neither the name") && !strings.Contains(lower, "gnu"):
		return "BSD-2-Clause"
	case strings.Contains(lower, "mozilla public license") && strings.Contains(lower, "2.0"):
		return "MPL-2.0"
	case strings.Contains(lower, "isc license"):
		return "ISC"
	case strings.Contains(lower, "the unlicense"):
		return "Unlicense"
	case strings.Contains(lower, "creative commons") && strings.Contains(lower, "attribution 4.0"):
		return "CC-BY-4.0"
	}
	return ""
}

// headLines returns the first n non-blank lines of text.
func headLines(text string, n int) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if out = append(out, l); len(out) == n {
			break
		}
	}
	return out
}
