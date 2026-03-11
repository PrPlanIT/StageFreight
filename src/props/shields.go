package props

import (
	"fmt"
	"strings"
)

const shieldsBase = "https://img.shields.io/"

// ShieldsResolver is a shared resolver for shields.io badge types.
// PathTemplate and LinkTemplate use {param_name} placeholders.
type ShieldsResolver struct {
	PathTemplate string            // e.g. "docker/pulls/{image}"
	LinkTemplate string            // e.g. "https://hub.docker.com/r/{image}"
	DefaultLogo  string            // e.g. "docker"
	Params       []ParamSpec
	Example      map[string]string
}

// Resolve constructs shields.io image and link URLs from params.
func (s ShieldsResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	// Apply defaults from ParamSpec for any missing optional params.
	merged := make(map[string]string, len(params))
	for _, p := range s.Params {
		if p.Default != "" {
			merged[p.Name] = p.Default
		}
	}
	for k, v := range params {
		merged[k] = v
	}

	imgPath := expandTemplate(s.PathTemplate, merged)
	linkURL := expandTemplate(s.LinkTemplate, merged)

	imgURL := shieldsBase + imgPath

	// Apply shields.io presentation overrides as query params.
	var qparts []string
	style := opts.Style
	if style != "" {
		qparts = append(qparts, "style="+style)
	}
	logo := opts.Logo
	if logo == "" {
		logo = s.DefaultLogo
	}
	if logo != "" {
		qparts = append(qparts, "logo="+logo)
	}
	if len(qparts) > 0 {
		imgURL += "?" + strings.Join(qparts, "&")
	}

	return ResolvedProp{
		ImageURL: imgURL,
		LinkURL:  linkURL,
	}, nil
}

// Schema returns param metadata for docs and validation.
func (s ShieldsResolver) Schema() PropSchema {
	return PropSchema{
		Params:  s.Params,
		Example: s.Example,
	}
}

// expandTemplate replaces {key} placeholders with param values.
// Values are inserted as-is — this is safe for structured values that naturally
// fit URL paths (repo: "owner/name", module: "github.com/org/name").
// For arbitrary text in URL path segments or query values, resolvers must
// apply encoding (url.PathEscape, url.QueryEscape, shieldsBadgeEscape) themselves.
func expandTemplate(tmpl string, params map[string]string) string {
	result := tmpl
	for k, v := range params {
		result = strings.ReplaceAll(result, fmt.Sprintf("{%s}", k), v)
	}
	return result
}
