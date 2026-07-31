package scribe

import (
	"strings"

	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/stencil"
)

// renderText is the unified render for a scribe placement body: stencil grammar
// FIRST (named {element} embeds + {#if} + memoization), then a gitver leaf fact-pass
// (ResolveTemplateWithDirAndVars: {base}/{env:}/{var:}/{project.*}/time/…, incl. its
// own {{}} literal escaping) for any vocab tokens the stencil left literal.
//
// The trailing TrimRight is load-bearing for byte-identity: stencil.Render appends a
// terminating newline (collapseBlankLines), but the old render.Compose/ComposeInline
// join had none, and registry.ReplaceBetween supplies its own boundary newlines — so
// an un-trimmed body would double the newline before a block end-marker and churn the
// file. Trimming restores the pre-refactor no-trailing-newline contract.
func renderText(body string, env stencil.Env, vi *gitver.VersionInfo, rootDir string, vars map[string]string) string {
	rendered := stencil.Expand(body, env)
	rendered = gitver.ResolveTemplateWithDirAndVars(rendered, vi, rootDir, vars)
	return strings.TrimRight(rendered, "\n")
}
