package upstream

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// PinValue is where one pin's version sits: the file, its 1-based line, the byte offset of
// the value within that line, and the value as written.
type PinValue struct {
	File   string
	Line   int
	Column int
	Value  string
	Pin    config.UpstreamPin
}

// Describe names the pin for messages.
func (p PinValue) Describe() string {
	switch {
	case p.Pin.Arg != "":
		return p.File + " " + p.Pin.Arg
	case p.Pin.Path != "":
		return p.File + " " + p.Pin.Path
	}
	return fmt.Sprintf("%s:%d", p.File, p.Line)
}

// MarkerText is the comment text that marks a marker pin's line for a source.
func MarkerText(sourceID string) string { return "stagefreight: upstream=" + sourceID }

// LocatePin finds the version a pin holds. root is the repository root.
func LocatePin(root, sourceID string, pin config.UpstreamPin) (PinValue, error) {
	data, err := os.ReadFile(filepath.Join(root, pin.File))
	if err != nil {
		return PinValue{}, fmt.Errorf("pin %s: %w", pin.File, err)
	}
	lines := strings.Split(string(data), "\n")
	out := PinValue{File: pin.File, Pin: pin}
	switch {
	case pin.Arg != "":
		re := regexp.MustCompile(`^\s*(?i:ARG|ENV)\s+` + regexp.QuoteMeta(pin.Arg) + `(?:=|\s+)["']?([^"'\s]+)`)
		for i, l := range lines {
			if m := re.FindStringSubmatchIndex(l); m != nil {
				out.Line, out.Column, out.Value = i+1, m[2], l[m[2]:m[3]]
				return out, nil
			}
		}
		return out, fmt.Errorf("pin %s: no ARG or ENV %s", pin.File, pin.Arg)
	case pin.Path != "":
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return out, fmt.Errorf("pin %s: %w", pin.File, err)
		}
		n, err := walkPath(&doc, pin.Path)
		if err != nil {
			return out, fmt.Errorf("pin %s %s: %w", pin.File, pin.Path, err)
		}
		if n.Line < 1 || n.Line > len(lines) {
			return out, fmt.Errorf("pin %s %s: value has no position", pin.File, pin.Path)
		}
		l := lines[n.Line-1]
		col := strings.Index(l[min(runeOffset(l, n.Column-1), len(l)):], n.Value)
		if col < 0 {
			return out, fmt.Errorf("pin %s %s: %q is not written literally on line %d", pin.File, pin.Path, n.Value, n.Line)
		}
		out.Line, out.Column, out.Value = n.Line, runeOffset(l, n.Column-1)+col, n.Value
		return out, nil
	case pin.Marker:
		match := pin.Match
		if match == "" {
			match = config.DefaultProbeMatch
		}
		re, err := regexp.Compile(match)
		if err != nil {
			return out, err
		}
		for i, l := range lines {
			if !strings.Contains(l, MarkerText(sourceID)) || i+1 >= len(lines) {
				continue
			}
			next := lines[i+1]
			m := re.FindStringSubmatchIndex(next)
			if m == nil {
				return out, fmt.Errorf("pin %s:%d: no version after the marker", pin.File, i+2)
			}
			g := 0
			if vi := re.SubexpIndex("version"); vi > 0 {
				g = vi
			} else if len(m) > 2 {
				g = 1
			}
			out.Line, out.Column, out.Value = i+2, m[2*g], next[m[2*g]:m[2*g+1]]
			return out, nil
		}
		return out, fmt.Errorf("pin %s: no line marked %q", pin.File, MarkerText(sourceID))
	}
	return out, fmt.Errorf("pin %s: set one of arg | path | marker", pin.File)
}

// walkPath follows a dotted path (map keys, numeric sequence indexes) to a scalar.
func walkPath(doc *yaml.Node, dotted string) (*yaml.Node, error) {
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		n = n.Content[0]
	}
	for _, key := range strings.Split(dotted, ".") {
		switch n.Kind {
		case yaml.MappingNode:
			var next *yaml.Node
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == key {
					next = n.Content[i+1]
				}
			}
			if next == nil {
				return nil, fmt.Errorf("no key %q", key)
			}
			n = next
		case yaml.SequenceNode:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(n.Content) {
				return nil, fmt.Errorf("no index %q", key)
			}
			n = n.Content[i]
		default:
			return nil, fmt.Errorf("%q is past a scalar", key)
		}
	}
	if n.Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("not a scalar")
	}
	return n, nil
}

// runeOffset converts a 0-based rune column (yaml.v3 positions count runes) to a byte offset.
func runeOffset(line string, runes int) int {
	for i := range line {
		if runes == 0 {
			return i
		}
		runes--
	}
	return len(line)
}

// RepinValue renders a target version in the style of the value it replaces: a v prefix is
// kept when the current value has one, and never added when it does not.
func RepinValue(current, target string) string {
	target = strings.TrimPrefix(target, "v")
	if strings.HasPrefix(current, "v") {
		return "v" + target
	}
	return target
}
