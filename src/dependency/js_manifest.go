package dependency

import (
	"encoding/json"
	"fmt"
	"strings"
)

// package.json edits for override floors. The file is edited as text so the operator's
// key order, indentation and formatting survive; each result is re-parsed before use.
// An object is addressed by its key path from the root ("overrides", or "pnpm",
// "overrides"). Only string members are read or written.

// jsonMember is one member of a JSON object: the key's opening quote through the end
// of its value, plus the value's own span.
type jsonMember struct {
	key                    string
	start, end             int // member span: key quote .. value end
	valueStart, valueEnd   int
	valueIsString          bool
	valueObjOpen, valueObj int // for an object value: '{' and '}' offsets
}

// jsonObject is an object's brace offsets and members.
type jsonObject struct {
	open, close int
	members     []jsonMember
}

// manifestOverride returns the string value of name in the object at path, if any.
func manifestOverride(raw []byte, path []string, name string) (string, bool) {
	obj, ok := findJSONObject(raw, path)
	if !ok {
		return "", false
	}
	for _, m := range obj.members {
		if m.key == name && m.valueIsString {
			var v string
			if json.Unmarshal(raw[m.valueStart:m.valueEnd], &v) == nil {
				return v, true
			}
		}
	}
	return "", false
}

// setManifestOverride sets name to value in the object at path, creating the object
// (and its parents) at the end of their enclosing object when absent. An existing
// member is replaced in place; a new one is appended to the object.
func setManifestOverride(raw []byte, path []string, name, value string) ([]byte, error) {
	enc, _ := json.Marshal(value)
	key, _ := json.Marshal(name)
	unit := detectIndentUnit(raw)

	// Walk down to the deepest existing object on the path.
	depth := 0
	parent, ok := findJSONObject(raw, nil)
	if !ok {
		return nil, fmt.Errorf("package.json is not a JSON object")
	}
	for depth < len(path) {
		next, found := findJSONObject(raw, path[:depth+1])
		if !found {
			break
		}
		parent = next
		depth++
	}

	if depth == len(path) {
		for _, m := range parent.members {
			if m.key == name {
				out := splice(raw, m.valueStart, m.valueEnd, string(enc))
				return validJSON(out)
			}
		}
		member := string(key) + ": " + string(enc)
		return validJSON(appendMember(raw, parent, member, depth+1, unit))
	}

	// Build the missing tail of the path as nested objects holding the member.
	body := string(key) + ": " + string(enc)
	for i := len(path) - 1; i >= depth; i-- {
		k, _ := json.Marshal(path[i])
		inner := strings.Repeat(unit, i+2)
		closing := strings.Repeat(unit, i+1)
		body = string(k) + ": {\n" + inner + body + "\n" + closing + "}"
	}
	return validJSON(appendMember(raw, parent, body, depth+1, unit))
}

// removeManifestOverride deletes name from the object at path, then removes any object
// on the path that the deletion left empty.
func removeManifestOverride(raw []byte, path []string, name string) ([]byte, error) {
	out := raw
	target := name
	for level := len(path); level >= 0; level-- {
		obj, ok := findJSONObject(out, path[:level])
		if !ok {
			return out, nil
		}
		idx := -1
		for i, m := range obj.members {
			if m.key == target {
				idx = i
			}
		}
		if idx < 0 {
			return validJSON(out)
		}
		out = deleteMember(out, obj, idx)
		if level == 0 {
			break
		}
		emptied, _ := findJSONObject(out, path[:level])
		if len(emptied.members) > 0 {
			break
		}
		target = path[level-1]
	}
	return validJSON(out)
}

func validJSON(b []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("package.json edit produced invalid JSON: %w", err)
	}
	return b, nil
}

func splice(raw []byte, from, to int, with string) []byte {
	out := make([]byte, 0, len(raw)+len(with))
	out = append(out, raw[:from]...)
	out = append(out, with...)
	return append(out, raw[to:]...)
}

// appendMember inserts member as the last member of obj at the given nesting level.
func appendMember(raw []byte, obj jsonObject, member string, level int, unit string) []byte {
	indent := strings.Repeat(unit, level)
	if len(obj.members) == 0 {
		closing := strings.Repeat(unit, level-1)
		return splice(raw, obj.open+1, obj.close, "\n"+indent+member+"\n"+closing)
	}
	last := obj.members[len(obj.members)-1]
	return splice(raw, last.end, last.end, ",\n"+indent+member)
}

// deleteMember removes member idx of obj with its separating comma.
func deleteMember(raw []byte, obj jsonObject, idx int) []byte {
	m := obj.members[idx]
	if len(obj.members) == 1 {
		return splice(raw, obj.open+1, obj.close, "")
	}
	if idx == 0 {
		next := obj.members[1]
		return splice(raw, m.start, next.start, "")
	}
	prev := obj.members[idx-1]
	return splice(raw, prev.end, m.end, "")
}

// detectIndentUnit returns the whitespace preceding the first member of the root
// object, defaulting to two spaces.
func detectIndentUnit(raw []byte) string {
	obj, ok := findJSONObject(raw, nil)
	if !ok || len(obj.members) == 0 {
		return "  "
	}
	i := obj.members[0].start
	j := i
	for j > 0 && (raw[j-1] == ' ' || raw[j-1] == '\t') {
		j--
	}
	if j == i || j == 0 || raw[j-1] != '\n' {
		return "  "
	}
	return string(raw[j:i])
}

// findJSONObject locates the object at path (nil = the root).
func findJSONObject(raw []byte, path []string) (jsonObject, bool) {
	p := &jsonScanner{b: raw}
	p.ws()
	if p.i >= len(raw) || raw[p.i] != '{' {
		return jsonObject{}, false
	}
	obj, err := p.object()
	if err != nil {
		return jsonObject{}, false
	}
	for _, key := range path {
		found := false
		for _, m := range obj.members {
			if m.key == key && m.valueObj > 0 {
				p := &jsonScanner{b: raw, i: m.valueObjOpen}
				sub, err := p.object()
				if err != nil {
					return jsonObject{}, false
				}
				obj, found = sub, true
				break
			}
		}
		if !found {
			return jsonObject{}, false
		}
	}
	return obj, true
}

// jsonScanner walks JSON text recording object member offsets.
type jsonScanner struct {
	b []byte
	i int
}

func (p *jsonScanner) ws() {
	for p.i < len(p.b) && strings.IndexByte(" \t\r\n", p.b[p.i]) >= 0 {
		p.i++
	}
}

func (p *jsonScanner) object() (jsonObject, error) {
	obj := jsonObject{open: p.i}
	p.i++ // '{'
	for {
		p.ws()
		if p.i >= len(p.b) {
			return obj, fmt.Errorf("unterminated object")
		}
		if p.b[p.i] == '}' {
			obj.close = p.i
			p.i++
			return obj, nil
		}
		if p.b[p.i] == ',' {
			p.i++
			continue
		}
		m := jsonMember{start: p.i}
		ks := p.i
		if err := p.str(); err != nil {
			return obj, err
		}
		if err := json.Unmarshal(p.b[ks:p.i], &m.key); err != nil {
			return obj, err
		}
		p.ws()
		if p.i >= len(p.b) || p.b[p.i] != ':' {
			return obj, fmt.Errorf("expected ':'")
		}
		p.i++
		p.ws()
		m.valueStart = p.i
		switch {
		case p.i < len(p.b) && p.b[p.i] == '"':
			m.valueIsString = true
			if err := p.str(); err != nil {
				return obj, err
			}
		case p.i < len(p.b) && p.b[p.i] == '{':
			m.valueObjOpen = p.i
			sub, err := p.object()
			if err != nil {
				return obj, err
			}
			m.valueObj = sub.close
		default:
			if err := p.skipValue(); err != nil {
				return obj, err
			}
			for p.i > m.valueStart && strings.IndexByte(" \t\r\n", p.b[p.i-1]) >= 0 {
				p.i--
			}
		}
		m.valueEnd = p.i
		m.end = p.i
		obj.members = append(obj.members, m)
	}
}

func (p *jsonScanner) str() error {
	if p.b[p.i] != '"' {
		return fmt.Errorf("expected string")
	}
	p.i++
	for p.i < len(p.b) {
		switch p.b[p.i] {
		case '\\':
			p.i += 2
		case '"':
			p.i++
			return nil
		default:
			p.i++
		}
	}
	return fmt.Errorf("unterminated string")
}

// skipValue passes over an array, number, literal or nested structure.
func (p *jsonScanner) skipValue() error {
	depth := 0
	for p.i < len(p.b) {
		c := p.b[p.i]
		switch {
		case c == '"':
			if err := p.str(); err != nil {
				return err
			}
			continue
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			if depth == 0 {
				return nil
			}
			depth--
			if depth == 0 {
				p.i++
				return nil
			}
		case c == ',' && depth == 0:
			return nil
		}
		p.i++
	}
	return fmt.Errorf("unterminated value")
}
