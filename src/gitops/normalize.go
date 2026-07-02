package gitops

import "strings"

// SchemaFinding is the structured, provenance-carrying form of a schema-validation
// finding. It separates what we can MECHANICALLY DERIVE from the validator output
// (kind, name, the offending field, the violated rule) from the raw validator
// transcript (the escape hatch). Field and Rule are best-effort: when the message
// can't be parsed with confidence they stay empty and the renderer falls back to Raw.
// The renderer must never synthesize meaning the transcript doesn't support — no
// operator folklore, only statements derivable from schema + manifest + result.
type SchemaFinding struct {
	Kind      string
	Name      string
	Version   string
	Field     string // dotted instance path incl. the offending property; "" if unparsed
	Rule      string // interpreted violation, phrased operator-side; "" if unparsed
	SchemaURL string // the schema authority the check ran against; "" if absent
	Raw       string // original validator message, verbatim (escape hatch)
}

// Parsed reports whether the violation was understood well enough to render an
// interpreted line. When false, callers show Raw rather than inventing meaning.
func (s SchemaFinding) Parsed() bool { return s.Field != "" || s.Rule != "" }

// kubeconformWrapper is kubeconform's generic (and misleading — it implies malformed
// JSON) prefix around the underlying jsonschema error. Stripped before parsing.
const kubeconformWrapper = "problem validating schema. Check JSON formatting: "

// parseSchemaViolation normalizes one kubeconform/jsonschema message into a
// SchemaFinding. Kind/Name/Version/Raw are ALWAYS set; Field/Rule/SchemaURL are
// filled only on a confident parse. kubeconform (wrapping santhosh-tekuri/jsonschema)
// emits:
//
//	problem validating schema. Check JSON formatting: jsonschema: '<pointer>' does not validate with <schema-url>#<rule-path>: <violation>
//
// e.g. jsonschema: '/spec/vaultContainerSpec' does not validate with
// https://.../vault_v1alpha1.json#/properties/spec/properties/vaultContainerSpec/required: missing properties: 'name'
func parseSchemaViolation(kind, name, version, raw string) SchemaFinding {
	sf := SchemaFinding{Kind: kind, Name: name, Version: version, Raw: strings.TrimSpace(raw)}

	msg := strings.TrimPrefix(sf.Raw, kubeconformWrapper)

	const marker = " does not validate with "
	i := strings.Index(msg, marker)
	if i < 0 {
		return sf // no recognizable structure → Raw-only, renderer falls back
	}
	left := strings.TrimSpace(msg[:i])
	right := strings.TrimSpace(msg[i+len(marker):])

	// left: "jsonschema: '<instance-pointer>'" → dotted field path
	field := dottedPath(firstQuoted(left))

	// right: "<schema-url>#<rule-path>: <violation>". Split on '#' first (the URL
	// itself contains ':' via https://, so a naive colon split is wrong).
	ruleTail := right
	if h := strings.Index(right, "#"); h >= 0 {
		sf.SchemaURL = strings.TrimSpace(right[:h])
		ruleTail = strings.TrimSpace(right[h+1:])
	}

	// ruleTail: "<rule-path>: <violation>". The rule-path's last segment is the
	// jsonschema keyword (required/type/additionalProperties/...).
	rulePath, violation := ruleTail, ""
	if c := strings.Index(ruleTail, ": "); c >= 0 {
		rulePath = strings.TrimSpace(ruleTail[:c])
		violation = strings.TrimSpace(ruleTail[c+2:])
	}

	switch lastSegment(rulePath) {
	case "required":
		// violation: "missing properties: 'name'[, 'x']" — append the offending
		// property name(s) to the instance path so the field points at what's missing.
		if props := allQuoted(violation); len(props) > 0 {
			joined := strings.Join(props, ", ")
			if field != "" {
				joined = field + "." + strings.Join(props, ", "+field+".")
			}
			sf.Field = joined
			sf.Rule = "required by schema, not set"
			return sf
		}
	}

	// Generic path: keep the instance field and quote the violation verbatim — honest,
	// not synthesized. Empty violation degrades to the raw rule tail.
	sf.Field = field
	if violation != "" {
		sf.Rule = violation
	} else if field == "" {
		sf.Rule = "" // nothing derivable → renderer uses Raw
	} else {
		sf.Rule = strings.TrimSpace(ruleTail)
	}
	return sf
}

// dottedPath converts a JSON pointer ("/spec/vaultContainerSpec") to a dotted field
// path ("spec.vaultContainerSpec"). Root ("" or "/") yields "".
func dottedPath(pointer string) string {
	p := strings.Trim(strings.TrimSpace(pointer), "/")
	if p == "" {
		return ""
	}
	return strings.ReplaceAll(p, "/", ".")
}

// firstQuoted returns the contents of the first single-quoted token, or "".
func firstQuoted(s string) string {
	a := strings.Index(s, "'")
	if a < 0 {
		return ""
	}
	b := strings.Index(s[a+1:], "'")
	if b < 0 {
		return ""
	}
	return s[a+1 : a+1+b]
}

// allQuoted returns the contents of every single-quoted token in order.
func allQuoted(s string) []string {
	var out []string
	for {
		a := strings.Index(s, "'")
		if a < 0 {
			return out
		}
		rest := s[a+1:]
		b := strings.Index(rest, "'")
		if b < 0 {
			return out
		}
		out = append(out, rest[:b])
		s = rest[b+1:]
	}
}

// lastSegment returns the final '/'-separated segment of a JSON pointer path.
func lastSegment(path string) string {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
