package props

// StaticResolver handles fixed badges with no dynamic provider params.
// The image URL and link URL are templates expanded with user params.
type StaticResolver struct {
	ImageURL string
	LinkURL  string
	Params   []ParamSpec
	Example  map[string]string
}

// Resolve returns the static image/link URLs, expanded with params.
func (s StaticResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	imgURL := expandTemplate(s.ImageURL, params)
	linkURL := expandTemplate(s.LinkURL, params)

	return ResolvedProp{
		ImageURL: imgURL,
		LinkURL:  linkURL,
	}, nil
}

// Schema returns param metadata for docs and validation.
func (s StaticResolver) Schema() PropSchema {
	return PropSchema{
		Params:  s.Params,
		Example: s.Example,
	}
}
