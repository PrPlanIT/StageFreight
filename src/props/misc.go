package props

import "fmt"

func init() {
	Register(Definition{
		ID:          "visitor-count",
		Format:      "badge",
		Category:    "misc",
		Description: "Page visitor counter badge",
		Provider:    ProviderNative,
		DefaultAlt:  "Visitor Count",
		Resolver:    &visitorCountResolver{},
	})
}

type visitorCountResolver struct{}

func (r *visitorCountResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	pageID := params["page_id"]
	return ResolvedProp{
		ImageURL: fmt.Sprintf("https://visitor-badge.laobi.icu/badge?page_id=%s", pageID),
		LinkURL:  "",
	}, nil
}

func (r *visitorCountResolver) Schema() PropSchema {
	return PropSchema{
		Params: []ParamSpec{
			{Name: "page_id", Required: true, Help: "Unique page identifier (e.g. prplanit.stagefreight)"},
		},
		Example: map[string]string{"page_id": "prplanit.stagefreight"},
	}
}
