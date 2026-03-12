package build

import "github.com/PrPlanIT/StageFreight/src/gitver"

// ResolveTemplate delegates to the gitver package.
func ResolveTemplate(tmpl string, v *gitver.VersionInfo) string {
	return gitver.ResolveTemplate(tmpl, v)
}

// ResolveTags delegates to the gitver package.
func ResolveTags(templates []string, v *gitver.VersionInfo) []string {
	return gitver.ResolveTags(templates, v)
}
