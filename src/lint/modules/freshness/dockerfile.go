package freshness

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// DockerFreshnessInfo holds everything extracted from a Dockerfile
// relevant to freshness checking.
type DockerFreshnessInfo struct {
	Stages      []stageInfo
	EnvVars     map[string]envVar
	PinnedTools []pinnedTool
	ApkPackages []packageRef
	AptPackages []packageRef
	PipPackages []packageRef
}

type stageInfo struct {
	Image string // full image reference (e.g. "golang:1.25-alpine")
	Name  string // AS alias
	Line  int
}

type envVar struct {
	Name  string
	Value string
	Line  int
}

type pinnedTool struct {
	EnvName string // e.g. "BUILDX_VERSION"
	Version string // e.g. "v0.31.1"
	Owner   string // GitHub owner
	Repo    string // GitHub repo
	Line    int    // line of the ENV declaration
}

type packageRef struct {
	Name    string
	Version string // empty if unpinned
	Line    int
}

var (
	// FROM [--platform=...] <image> [AS <name>]
	fromRe = regexp.MustCompile(`(?i)^FROM\s+(?:--platform=\S+\s+)?(\S+)(?:\s+AS\s+(\S+))?`)
	// ARG KEY=VALUE
	argRe = regexp.MustCompile(`(?i)^ARG\s+(\S+?)=(.+)`)
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
func parseDockerfileForFreshness(path string) (*DockerFreshnessInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info := &DockerFreshnessInfo{
		EnvVars: make(map[string]envVar),
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
			stage := stageInfo{Image: m[1], Line: endLine}
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

		// ARG (only for *_VERSION patterns)
		if m := argRe.FindStringSubmatch(line); m != nil {
			name := m[1]
			value := strings.TrimSpace(m[2])
			value = strings.Trim(value, `"'`)
			if strings.HasSuffix(strings.ToUpper(name), "_VERSION") {
				info.EnvVars[name] = envVar{Name: name, Value: value, Line: endLine}
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

	return info, nil
}

// parseEnvLine handles both Docker ENV syntaxes:
//
//	Old-style (single var):  ENV KEY VALUE WITH SPACES
//	New-style (multi-var):   ENV K1=V1 K2=V2 K3="value with spaces"
//
// New-style is detected by the presence of '=' in the first token.
func parseEnvLine(info *DockerFreshnessInfo, body string, line int) {
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
		info.EnvVars[name] = envVar{Name: name, Value: value, Line: line}
		return
	}

	if firstSpace >= 0 && firstSpace < firstEquals {
		// Space comes before equals: old-style "ENV KEY VALUE=SOMETHING"
		name := body[:firstSpace]
		value := strings.TrimSpace(body[firstSpace+1:])
		value = strings.Trim(value, `"'`)
		info.EnvVars[name] = envVar{Name: name, Value: value, Line: line}
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

		info.EnvVars[name] = envVar{Name: name, Value: value, Line: line}
	}
}

// parseRunLine extracts package installs and GitHub URLs from a RUN instruction body.
func parseRunLine(info *DockerFreshnessInfo, body string, line int) {
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
func parsePackageList(raw string, versionSep string) []packageRef {
	var refs []packageRef
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
		pr := packageRef{}
		if idx := strings.Index(field, versionSep); idx >= 0 {
			pr.Name = field[:idx]
			pr.Version = field[idx+len(versionSep):]
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
func crossRefTools(info *DockerFreshnessInfo) []pinnedTool {
	// Collect all GitHub owner/repo pairs from the Dockerfile.
	// We scan the raw stages aren't enough — we need the full RUN lines.
	// Re-read isn't needed since we already have EnvVars.
	var tools []pinnedTool

	for name, ev := range info.EnvVars {
		if !strings.HasSuffix(strings.ToUpper(name), "_VERSION") {
			continue
		}
		// For now, record the tool. The GitHub owner/repo resolution
		// happens in tools.go where we have the full file content.
		tools = append(tools, pinnedTool{
			EnvName: name,
			Version: ev.Value,
			Line:    ev.Line,
		})
	}

	return tools
}
