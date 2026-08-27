package cmd

import (
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/presetfetch"
)

// init wires the network-capable preset fetcher into config's set-once SourceFetcher
// seam, so a sourced preset ref (<source>//<path>[@<ref>]) resolves by fetching from its
// source. Only the CLI (a network-capable entry point) does this; other contexts leave
// SourceFetcher nil and resolve sourced refs from the preset-cache only.
func init() {
	config.SourceFetcher = presetfetch.New(resolvePresetSource)
}

// resolvePresetSource maps a preset ref's <source> to a clonable repo URL. Forge-shorthand
// sources ("gitlab:Org/Repo") are resolved to URLs earlier, in the config loader, from the
// config's forges: block — so by the time the fetcher runs, a source is normally a URL. A
// URL (with a scheme, or an scp-like git remote) passes through here; a residual bare
// shorthand (an unknown forge id) returns a clear error rather than a wrong clone.
func resolvePresetSource(source string) (string, error) {
	switch {
	case strings.Contains(source, "://"):
		return source, nil
	case strings.Contains(source, "@") && strings.Contains(source, ":"):
		return source, nil // scp-like: git@host:org/repo
	default:
		return "", fmt.Errorf("preset source %q is a forge shorthand; use a full repo URL (forge-shorthand resolution is not yet wired)", source)
	}
}
