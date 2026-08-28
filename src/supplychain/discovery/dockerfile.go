package discovery

import (
	"bufio"
	"os"
	"regexp"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

var (
	// FROM [--platform=...] <image> [AS <name>]
	fromRe = regexp.MustCompile(`(?i)^FROM\s+(?:--platform=\S+\s+)?(\S+)(?:\s+AS\s+(\S+))?`)
	// ARG KEY=VALUE
	argRe = regexp.MustCompile(`(?i)^ARG\s+(\S+?)=(.+)`)
	// Shell-style variable reference in a FROM image token:
	//   ${VAR}   ${VAR:-default}   $VAR
	// Group 1/2 = braced name/inline-default; group 3 = bare name.
	varRefRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
	// GitHub release download patterns in wget/curl commands
	githubReleaseRe = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/releases/download/`)
	// apk add [options] pkg1[=ver] pkg2[=ver] ...
	apkAddRe = regexp.MustCompile(`(?i)apk\s+(?:--no-cache\s+)?add\s+(.+)`)
	// apt-get install [options] pkg1[=ver] pkg2[=ver] ...
	aptInstallRe = regexp.MustCompile(`(?i)apt-get\s+install\s+(?:-y\s+)?(?:--no-install-recommends\s+)?(.+)`)
	// pip install [options] pkg1[==ver] pkg2[==ver] ...
	pipInstallRe = regexp.MustCompile(`(?i)pip3?\s+install\s+(?:--no-cache-dir\s+)?(.+)`)
)

// parseDockerfileForFreshness does a richer parse than build.ParseDockerfile,
// extracting ENV vars, RUN-line package installs, and pinned tool patterns.
func parseDockerfileForFreshness(path string) (*supplychain.DockerFreshnessInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info := &supplychain.DockerFreshnessInfo{
		EnvVars: make(map[string]supplychain.EnvVar),
		Args:    make(map[string]supplychain.EnvVar),
	}

	scanner := bufio.NewScanner(f)
	lineNum := 0
	var continuation strings.Builder

	flushLine := func(line string, endLine int) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			return
		}

		// FROM
		if m := fromRe.FindStringSubmatch(line); m != nil {
			stage := supplychain.StageInfo{Image: m[1], Line: endLine}
			if len(m) > 2 {
				stage.Name = m[2]
			}
			info.Stages = append(info.Stages, stage)
			return
		}

		// ENV — handles both old-style (ENV KEY VALUE) and new-style
		// multi-var (ENV K1=V1 K2=V2 K3=V3)
		if strings.HasPrefix(strings.ToUpper(line), "ENV ") {
			parseEnvLine(info, line[4:], endLine)
			return
		}

		// ARG — record every declaration in Args so a ${VAR} base image resolves,
		// and additionally mirror *_VERSION entries into EnvVars for tool cross-referencing.
		if m := argRe.FindStringSubmatch(line); m != nil {
			name := m[1]
			value := strings.TrimSpace(m[2])
			value = strings.Trim(value, `"'`)
			info.Args[name] = supplychain.EnvVar{Name: name, Value: value, Line: endLine}
			if strings.HasSuffix(strings.ToUpper(name), "_VERSION") {
				info.EnvVars[name] = supplychain.EnvVar{Name: name, Value: value, Line: endLine}
			}
			return
		}

		// RUN lines — check for package managers and tool downloads
		if strings.HasPrefix(strings.ToUpper(line), "RUN ") {
			runBody := line[4:]
			parseRunLine(info, runBody, endLine)
		}
	}

	for scanner.Scan() {
		lineNum++
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)

		if strings.HasSuffix(trimmed, `\`) {
			// Line continuation
			continuation.WriteString(strings.TrimSuffix(trimmed, `\`))
			continuation.WriteByte(' ')
			continue
		}

		if continuation.Len() > 0 {
			continuation.WriteString(trimmed)
			flushLine(continuation.String(), lineNum)
			continuation.Reset()
		} else {
			flushLine(trimmed, lineNum)
		}
	}

	// Flush any remaining continuation
	if continuation.Len() > 0 {
		flushLine(continuation.String(), lineNum)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Cross-reference ENV *_VERSION vars with GitHub URLs
	info.PinnedTools = crossRefTools(info)

	// Package pins are resolved AFTER the full scan: a RUN line may reference an ARG
	// declared anywhere in the file, so resolving inline would depend on declaration
	// order. Without this a pin written as pkg="${PKG_VERSION}" reaches the freshness
	// check as the literal string "${PKG_VERSION}", matches no upstream version, and
	// the package is silently untracked — the common shape for a version an operator
	// wants in one place at the top of the Dockerfile.
	resolvePackageVersions(info, info.AptPackages)
	resolvePackageVersions(info, info.ApkPackages)
	resolvePackageVersions(info, info.PipPackages)

	return info, nil
}

// resolvePackageVersions substitutes ${VAR}/$VAR in package version pins from the
// Dockerfile's own ARG/ENV declarations, recording the variable as the editable anchor.
// A version that cannot be fully resolved is cleared rather than left as a template:
// an unresolvable pin is not a version, and reporting it as one produces a bogus
// "outdated" comparison against every real upstream version.
func resolvePackageVersions(info *supplychain.DockerFreshnessInfo, pkgs []supplychain.PackageRef) {
	for i := range pkgs {
		v := pkgs[i].Version
		if v == "" || !strings.Contains(v, "$") {
			continue
		}
		var binding string
		var bindingLine int
		unresolved := false
		resolved := varRefRe.ReplaceAllStringFunc(v, func(match string) string {
			m := varRefRe.FindStringSubmatch(match)
			name := m[1]
			if name == "" {
				name = m[3] // bare $VAR form
			}
			if ev, found := lookupDockerVar(info, name); found {
				if binding == "" {
					binding = name
					bindingLine = ev.Line
				}
				return ev.Value
			}
			if m[2] != "" {
				return m[2] // inline default ${VAR:-1.2.3}
			}
			unresolved = true
			return match
		})
		if unresolved {
			pkgs[i].Version = ""
			continue
		}
		pkgs[i].Version = resolved
		pkgs[i].Binding = binding
		pkgs[i].BindingLine = bindingLine
	}
}

// parseEnvLine handles both Docker ENV syntaxes:
//
//	Old-style (single var):  ENV KEY VALUE WITH SPACES
//	New-style (multi-var):   ENV K1=V1 K2=V2 K3="value with spaces"
//
// New-style is detected by the presence of '=' in the first token.
func parseEnvLine(info *supplychain.DockerFreshnessInfo, body string, line int) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}

	// Check if this is new-style (KEY=VALUE pairs) by looking for '=' in the first token.
	firstSpace := strings.IndexByte(body, ' ')
	firstEquals := strings.IndexByte(body, '=')

	if firstEquals < 0 {
		// No equals sign at all: old-style "ENV KEY VALUE"
		// The first token is the key, the rest is the value.
		if firstSpace < 0 {
			// "ENV KEY" with no value
			return
		}
		name := body[:firstSpace]
		value := strings.TrimSpace(body[firstSpace+1:])
		value = strings.Trim(value, `"'`)
		info.EnvVars[name] = supplychain.EnvVar{Name: name, Value: value, Line: line}
		return
	}

	if firstSpace >= 0 && firstSpace < firstEquals {
		// Space comes before equals: old-style "ENV KEY VALUE=SOMETHING"
		name := body[:firstSpace]
		value := strings.TrimSpace(body[firstSpace+1:])
		value = strings.Trim(value, `"'`)
		info.EnvVars[name] = supplychain.EnvVar{Name: name, Value: value, Line: line}
		return
	}

	// New-style: KEY1=VALUE1 KEY2=VALUE2 ...
	// Parse each KEY=VALUE pair, respecting quoted values.
	for body != "" {
		body = strings.TrimSpace(body)
		if body == "" {
			break
		}

		eqIdx := strings.IndexByte(body, '=')
		if eqIdx < 0 {
			break
		}

		name := body[:eqIdx]
		rest := body[eqIdx+1:]

		var value string
		if len(rest) > 0 && (rest[0] == '"' || rest[0] == '\'') {
			// Quoted value — find matching close quote.
			quote := rest[0]
			end := strings.IndexByte(rest[1:], quote)
			if end < 0 {
				// Unterminated quote — take everything.
				value = rest[1:]
				body = ""
			} else {
				value = rest[1 : end+1]
				body = rest[end+2:]
			}
		} else {
			// Unquoted value — ends at next whitespace.
			spIdx := strings.IndexAny(rest, " \t")
			if spIdx < 0 {
				value = rest
				body = ""
			} else {
				value = rest[:spIdx]
				body = rest[spIdx+1:]
			}
		}

		info.EnvVars[name] = supplychain.EnvVar{Name: name, Value: value, Line: line}
	}
}

// parseRunLine extracts package installs and GitHub URLs from a RUN instruction body.
func parseRunLine(info *supplychain.DockerFreshnessInfo, body string, line int) {
	// Split on && to handle chained commands
	cmds := strings.Split(body, "&&")
	for _, cmd := range cmds {
		cmd = strings.TrimSpace(cmd)

		// APK
		if m := apkAddRe.FindStringSubmatch(cmd); m != nil {
			for _, pkg := range parsePackageList(m[1], "=") {
				pkg.Line = line
				info.ApkPackages = append(info.ApkPackages, pkg)
			}
		}

		// APT
		if m := aptInstallRe.FindStringSubmatch(cmd); m != nil {
			for _, pkg := range parsePackageList(m[1], "=") {
				pkg.Line = line
				info.AptPackages = append(info.AptPackages, pkg)
			}
		}

		// pip
		if m := pipInstallRe.FindStringSubmatch(cmd); m != nil {
			for _, pkg := range parsePackageList(m[1], "==") {
				pkg.Line = line
				info.PipPackages = append(info.PipPackages, pkg)
			}
		}
	}
}

// parsePackageList splits "pkg1=ver pkg2 pkg3=ver" into packageRefs.
func parsePackageList(raw string, versionSep string) []supplychain.PackageRef {
	var refs []supplychain.PackageRef
	fields := strings.Fields(raw)
	for _, field := range fields {
		// Skip flags like --no-cache, -y, etc.
		if strings.HasPrefix(field, "-") {
			continue
		}
		// Skip line continuation artifacts
		if field == `\` {
			continue
		}
		pr := supplychain.PackageRef{}
		if idx := strings.Index(field, versionSep); idx >= 0 {
			pr.Name = field[:idx]
			// Quotes are shell syntax, not part of the version. A pin written
			// pkg="${VER}" (quoted so the shell keeps a value containing spaces or
			// globs intact) otherwise carries the quotes into the comparison, where
			// "3.7.5-1" never equals the archive's 3.7.5-1 and the package reads as
			// perpetually mismatched.
			pr.Version = strings.Trim(field[idx+len(versionSep):], `"'`)
		} else {
			pr.Name = field
		}
		// Filter out empty names and things that look like pipes/redirects
		if pr.Name != "" && !strings.ContainsAny(pr.Name, "|><&;") {
			refs = append(refs, pr)
		}
	}
	return refs
}

// crossRefTools matches ENV *_VERSION variables with GitHub release URLs
// found in RUN lines to identify pinned tool versions.
func crossRefTools(info *supplychain.DockerFreshnessInfo) []supplychain.PinnedTool {
	// Collect all GitHub owner/repo pairs from the Dockerfile.
	// We scan the raw stages aren't enough — we need the full RUN lines.
	// Re-read isn't needed since we already have EnvVars.
	var tools []supplychain.PinnedTool

	for name, ev := range info.EnvVars {
		if !strings.HasSuffix(strings.ToUpper(name), "_VERSION") {
			continue
		}
		// Skip *_VERSION entries whose value is a branch/ref rather than a
		// version (e.g. "develop", "master", an arbitrary branch). These are
		// not updatable dependencies and must never be rewritten to a release tag.
		if !version.IsVersionLike(ev.Value) {
			continue
		}
		// For now, record the tool. The GitHub owner/repo resolution
		// happens in tools.go where we have the full file content.
		tools = append(tools, supplychain.PinnedTool{
			EnvName: name,
			Version: ev.Value,
			Line:    ev.Line,
		})
	}

	return tools
}

// resolveImageRef substitutes ${VAR} / ${VAR:-default} / $VAR references in a FROM
// image token with values declared in the Dockerfile, giving the effective image the
// build actually pulls. Each reference resolves to its ARG default, then its ENV
// default, then an inline `:-default`. When a reference resolves through an ARG/ENV
// definition, that line is the editable anchor: binding names the variable and line is
// its declaration, so an update lands on `ARG VAR=…` rather than the FROM. A reference
// satisfied only by an inline default leaves binding empty — its version lives in the
// FROM token itself. ok is false only when a reference has no resolvable value at all
// (the sole legitimate reason to skip an interpolated base image).
func resolveImageRef(image string, info *supplychain.DockerFreshnessInfo) (resolved, binding string, line int, ok bool) {
	if !strings.Contains(image, "$") {
		return image, "", 0, true
	}
	unresolved := false
	resolved = varRefRe.ReplaceAllStringFunc(image, func(match string) string {
		m := varRefRe.FindStringSubmatch(match)
		name := m[1]
		if name == "" {
			name = m[3] // bare $VAR form
		}
		if ev, found := lookupDockerVar(info, name); found {
			// Record the first ARG/ENV-anchored variable as the editable anchor.
			if binding == "" {
				binding = name
				line = ev.Line
			}
			return ev.Value
		}
		if m[2] != "" {
			// Inline default (${VAR:-default}) — the version is embedded in the FROM.
			return m[2]
		}
		unresolved = true
		return match
	})
	if unresolved {
		return image, "", 0, false
	}
	return resolved, binding, line, true
}

// lookupDockerVar resolves a Dockerfile variable to its declared value, preferring an
// ARG default over an ENV default (a FROM interpolation is a build-time ARG substitution).
func lookupDockerVar(info *supplychain.DockerFreshnessInfo, name string) (supplychain.EnvVar, bool) {
	if ev, ok := info.Args[name]; ok {
		return ev, true
	}
	if ev, ok := info.EnvVars[name]; ok {
		return ev, true
	}
	return supplychain.EnvVar{}, false
}

// pkgEditLine reports the line an update to this package must rewrite: the ARG/ENV
// declaration when the version is bound to one, else the install line itself.
func pkgEditLine(pkg supplychain.PackageRef) int {
	if pkg.Binding != "" && pkg.BindingLine > 0 {
		return pkg.BindingLine
	}
	return pkg.Line
}
