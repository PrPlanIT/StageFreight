package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/parser"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("dockerfile-cache", func() lint.Module {
		return &dockerCacheModule{volatile: defaultVolatileArgs}
	})
}

// dockerCacheModule reports Dockerfile layouts that waste BuildKit's cache on repeat
// builds: source copied ahead of a dependency install, package caches without a cache
// mount, and a per-build ARG declared ahead of steps that don't use it. Advice only: it
// never fails a build.
type dockerCacheModule struct {
	sev      lint.Severity
	volatile []*regexp.Regexp
}

// defaultVolatileArgs match ARGs whose value changes on every build.
var defaultVolatileArgs = compileArgPatterns([]string{
	`VERSION`, `.*_VERSION`, `COMMIT`, `.*_COMMIT`, `GIT_.*`, `SHA`, `.*_SHA`, `REVISION`,
	`BUILD_(DATE|TIME|NUMBER|ID)`, `CI_.*`, `SOURCE_DATE_EPOCH`,
})

func compileArgPatterns(patterns []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		out = append(out, regexp.MustCompile(`^(`+p+`)$`))
	}
	return out
}

func (m *dockerCacheModule) Name() string         { return "dockerfile-cache" }
func (m *dockerCacheModule) DefaultEnabled() bool { return true }
func (m *dockerCacheModule) AutoDetect() []string { return []string{"Dockerfile*", "*.dockerfile"} }

// Configure accepts severity (info | warning) and volatile_args: extra ARG name patterns
// (regular expressions) whose value changes every build.
func (m *dockerCacheModule) Configure(opts map[string]any) error {
	var cfg struct {
		Severity     string   `json:"severity"`
		VolatileArgs []string `json:"volatile_args"`
	}
	if b, err := json.Marshal(opts); err != nil {
		return fmt.Errorf("dockerfile-cache: options: %w", err)
	} else if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("dockerfile-cache: options: %w", err)
	}
	switch cfg.Severity {
	case "", "info":
		m.sev = lint.SeverityInfo
	case "warning":
		m.sev = lint.SeverityWarning
	default:
		return fmt.Errorf("dockerfile-cache: severity %q must be info or warning", cfg.Severity)
	}
	for _, p := range cfg.VolatileArgs {
		re, err := regexp.Compile(`^(` + p + `)$`)
		if err != nil {
			return fmt.Errorf("dockerfile-cache: volatile_args %q: %w", p, err)
		}
		m.volatile = append(m.volatile, re)
	}
	return nil
}

// installRe matches a dependency install a manifest alone determines.
var installRe = regexp.MustCompile(`\b(go\s+mod\s+download|npm\s+ci|npm\s+install\s*($|&&|;|\|)|yarn(\s+install)?\s*($|&&|;|\|)|pnpm\s+install|pip3?\s+install\s+.*-r\s|poetry\s+install|cargo\s+fetch|bundle\s+install|composer\s+install)`)

// cacheTools are package managers whose download/build cache a cache mount keeps across
// builds.
var cacheTools = []struct {
	name, target string
	re           *regexp.Regexp
}{
	{"go", "/root/.cache/go-build and /go/pkg/mod", regexp.MustCompile(`\bgo\s+(build|test|install|mod\s+download|generate)\b`)},
	{"npm", "/root/.npm", regexp.MustCompile(`\bnpm\s+(ci|install)\b`)},
	{"yarn", "/usr/local/share/.cache/yarn", regexp.MustCompile(`\byarn(\s+install)?\s*($|&&|;|\|)`)},
	{"pnpm", "/root/.local/share/pnpm/store", regexp.MustCompile(`\bpnpm\s+install\b`)},
	{"pip", "/root/.cache/pip", regexp.MustCompile(`\bpip3?\s+install\b`)},
	{"cargo", "/usr/local/cargo/registry and the target directory", regexp.MustCompile(`\bcargo\s+(build|fetch|install)\b`)},
}

// pendingArg is a per-build ARG declared in the current stage and not yet used.
type pendingArg struct {
	line int
	runs int // RUN steps since the declaration that don't use it
}

func (m *dockerCacheModule) Check(_ context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	data, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return nil, err
	}
	res, err := parser.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, nil // a Dockerfile BuildKit can't parse is the build's to report
	}

	var findings []lint.Finding
	add := func(line int, rule, msg string) {
		findings = append(findings, lint.Finding{
			File: file.Path, Line: line, Module: m.Name(), Severity: m.sev, RuleID: rule, Message: msg,
		})
	}

	var contextCopy int // line of COPY . in this stage, 0 if none
	installFlagged := false
	pending := map[string]*pendingArg{}
	order := []string{}
	flushArgs := func() {
		for _, name := range order {
			if p := pending[name]; p != nil && p.runs > 0 {
				add(p.line, "volatile-arg-early", fmt.Sprintf(
					"ARG %s changes every build, and the %d RUN step(s) after it that don't use it re-run with it; declare it just before its first use", name, p.runs))
			}
		}
		pending, order = map[string]*pendingArg{}, nil
	}

	for _, n := range res.AST.Children {
		cmd := strings.ToLower(n.Value)
		args := nodeArgs(n)
		text := strings.Join(args, " ")
		for _, h := range n.Heredocs {
			text += "\n" + h.Content
		}

		// Any instruction that expands a pending ARG is its first use.
		for _, name := range order {
			if p := pending[name]; p != nil && referencesArg(text, name) {
				if p.runs > 0 {
					add(p.line, "volatile-arg-early", fmt.Sprintf(
						"ARG %s changes every build, and the %d RUN step(s) before its first use re-run with it; declare it just before line %d", name, p.runs, n.StartLine))
				}
				pending[name] = nil
			}
		}

		switch cmd {
		case "from":
			flushArgs()
			contextCopy, installFlagged = 0, false
		case "copy", "add":
			if hasFlag(n.Flags, "--from") || len(args) < 2 {
				break
			}
			for _, src := range args[:len(args)-1] {
				if src == "." || src == "./" {
					contextCopy = n.StartLine
				}
			}
		case "arg":
			for _, a := range args {
				name, _, _ := strings.Cut(a, "=")
				if m.isVolatile(name) && pending[name] == nil {
					pending[name] = &pendingArg{line: n.StartLine}
					order = append(order, name)
				}
			}
		case "run":
			if contextCopy > 0 && !installFlagged && installRe.MatchString(text) {
				add(n.StartLine, "source-before-install", fmt.Sprintf(
					"COPY . on line %d comes before this dependency install, so any source change reinstalls every dependency; copy the manifests first, install, then copy the source", contextCopy))
				installFlagged = true
			}
			if !hasCacheMount(n.Flags) {
				for _, t := range cacheTools {
					if t.re.MatchString(text) {
						add(n.StartLine, "no-cache-mount", fmt.Sprintf(
							"this %s step has no cache mount, so every build downloads and rebuilds from nothing; add --mount=type=cache,target=… for %s", t.name, t.target))
						break
					}
				}
			}
			for _, name := range order {
				if p := pending[name]; p != nil {
					p.runs++
				}
			}
		}
	}
	flushArgs()
	return findings, nil
}

func (m *dockerCacheModule) isVolatile(name string) bool {
	for _, re := range m.volatile {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

// nodeArgs returns an instruction's arguments, flags excluded.
func nodeArgs(n *parser.Node) []string {
	var out []string
	for a := n.Next; a != nil; a = a.Next {
		out = append(out, a.Value)
	}
	return out
}

func hasFlag(flags []string, name string) bool {
	for _, f := range flags {
		if f == name || strings.HasPrefix(f, name+"=") {
			return true
		}
	}
	return false
}

func hasCacheMount(flags []string) bool {
	for _, f := range flags {
		if strings.HasPrefix(f, "--mount=") && strings.Contains(f, "type=cache") {
			return true
		}
	}
	return false
}

func referencesArg(text, name string) bool {
	return strings.Contains(text, "${"+name) || regexp.MustCompile(`\$`+regexp.QuoteMeta(name)+`\b`).MatchString(text)
}
