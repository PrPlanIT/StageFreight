package props

import (
	"fmt"
	"strings"
)

// ValidationError reports a problem with prop params or resolution.
type ValidationError struct {
	TypeID  string
	Param   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Param != "" {
		return fmt.Sprintf("props[%s].params.%s: %s", e.TypeID, e.Param, e.Message)
	}
	return fmt.Sprintf("props[%s]: %s", e.TypeID, e.Message)
}

// ValidateParams checks params against a resolver's Schema().
// Missing required params and unknown params produce errors.
func ValidateParams(def Definition, params map[string]string) error {
	schema := def.Resolver.Schema()

	// Build known param set.
	known := make(map[string]bool, len(schema.Params))
	for _, p := range schema.Params {
		known[p.Name] = true
	}

	// Check for unknown params.
	for k := range params {
		if !known[k] {
			return &ValidationError{
				TypeID:  def.ID,
				Param:   k,
				Message: "unknown parameter",
			}
		}
	}

	// Check required params.
	for _, p := range schema.Params {
		if p.Required {
			if _, ok := params[p.Name]; !ok {
				return &ValidationError{
					TypeID:  def.ID,
					Param:   p.Name,
					Message: "required parameter missing",
				}
			}
		}
	}

	return nil
}

// ResolveDefinition is the single safe entry point for narrator and CLI.
//  1. ValidateParams
//  2. Resolver.Resolve(params, opts)
//  3. Sanity-check output (no unresolved {placeholder} tokens)
//  4. Fill Alt from Definition.DefaultAlt if resolver returned empty
//  5. Apply RenderOptions overrides (Label->Alt, Link->LinkURL)
func ResolveDefinition(def Definition, params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	// Validate variant before anything else.
	if err := ValidateVariant(opts.Variant); err != nil {
		return ResolvedProp{}, &ValidationError{
			TypeID:  def.ID,
			Message: err.Error(),
		}
	}

	if err := ValidateParams(def, params); err != nil {
		return ResolvedProp{}, err
	}

	result, err := def.Resolver.Resolve(params, opts)
	if err != nil {
		return ResolvedProp{}, &ValidationError{
			TypeID:  def.ID,
			Message: fmt.Sprintf("resolve failed: %v", err),
		}
	}

	// Sanity-check: no unresolved {placeholder} tokens.
	if containsPlaceholder(result.ImageURL) {
		return ResolvedProp{}, &ValidationError{
			TypeID:  def.ID,
			Message: fmt.Sprintf("unresolved placeholder in ImageURL: %s", result.ImageURL),
		}
	}
	if containsPlaceholder(result.LinkURL) {
		return ResolvedProp{}, &ValidationError{
			TypeID:  def.ID,
			Message: fmt.Sprintf("unresolved placeholder in LinkURL: %s", result.LinkURL),
		}
	}

	// Fill Alt from DefaultAlt if empty.
	if result.Alt == "" {
		result.Alt = def.DefaultAlt
	}

	// Apply overrides.
	if opts.Label != "" {
		result.Alt = opts.Label
	}
	if opts.Link != "" {
		result.LinkURL = opts.Link
	}

	return result, nil
}

// containsPlaceholder checks for unresolved {name} tokens in a string.
// Ignores URL query parameters which legitimately contain {}.
func containsPlaceholder(s string) bool {
	// Look for {word} patterns that aren't part of URL query strings.
	inQuery := false
	for i := 0; i < len(s); i++ {
		if s[i] == '?' {
			inQuery = true
		}
		if s[i] == '{' && !inQuery {
			// Find closing brace.
			for j := i + 1; j < len(s); j++ {
				if s[j] == '}' {
					token := s[i+1 : j]
					if isPlaceholderToken(token) {
						return true
					}
					break
				}
			}
		}
	}
	return false
}

// isPlaceholderToken returns true if the token looks like an unresolved placeholder
// (simple word characters, not a URL-encoded value).
func isPlaceholderToken(token string) bool {
	if token == "" {
		return false
	}
	for _, c := range token {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	// Ignore common false positives.
	return !strings.EqualFold(token, "")
}
