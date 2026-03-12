package narrator

import "github.com/PrPlanIT/StageFreight/src/props"

// PropsModule renders a resolved prop through the shared FormatMarkdown path.
// The prop is resolved before this module is created.
type PropsModule struct {
	Resolved props.ResolvedProp
	Variant  props.Variant
}

// Render produces the inline markdown for this prop via the shared render path.
func (p PropsModule) Render() string {
	return props.FormatMarkdown(p.Resolved, p.Variant)
}
