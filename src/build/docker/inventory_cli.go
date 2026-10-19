package docker

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// ── Shell parsing ────────────────────────────────────────────────────────────

// splitShellCommands splits a RUN body into its simple commands at &&, ||, ;, | and &,
// outside quotes. The & of a redirection (2>&1, &>file) does not split.
func splitShellCommands(s string) []string {
	var result []string
	var current strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			current.WriteByte(c)
			if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				current.WriteByte(s[i])
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			current.WriteByte(c)
		case '\\':
			current.WriteByte(c)
			if i+1 < len(s) {
				i++
				current.WriteByte(s[i])
			}
		case ';', '|', '&':
			if c == '&' && ((i > 0 && s[i-1] == '>') || (i+1 < len(s) && s[i+1] == '>')) {
				current.WriteByte(c)
				continue
			}
			result = append(result, current.String())
			current.Reset()
			if i+1 < len(s) && s[i+1] == c {
				i++
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		result = append(result, current.String())
	}
	return result
}

// shellWords splits one simple command into words the way the shell does: at unquoted
// whitespace, with quotes removed and backslash escapes applied. Variables stay unexpanded.
func shellWords(s string) []string {
	var words []string
	var current strings.Builder
	inWord := false
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				current.WriteByte(c)
			}
		case quote == '"':
			if c == '"' {
				quote = 0
			} else if c == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
				i++
				current.WriteByte(s[i])
			} else {
				current.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote = c
			inWord = true
		case c == '\\' && i+1 < len(s):
			i++
			current.WriteByte(s[i])
			inWord = true
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, current.String())
				current.Reset()
				inWord = false
			}
		default:
			current.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, current.String())
	}
	return words
}

var (
	envAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	pythonRe    = regexp.MustCompile(`^python[0-9.]*$`)
	// redirectRe matches a redirection word; redirectBareRe one whose target is the next word.
	redirectRe     = regexp.MustCompile(`^[0-9]*(?:>>?|<<?|&>>?|>&|<&)`)
	redirectBareRe = regexp.MustCompile(`^[0-9]*(?:>>?|<<?|&>>?)$`)
)

// commandWrappers run the command that follows them.
var commandWrappers = map[string]bool{
	"sudo": true, "env": true, "command": true, "exec": true, "nohup": true, "time": true, "nice": true,
}

// commandWords returns the words of a simple command starting at the program it runs:
// leading RUN flags (--mount=…), variable assignments and wrappers (sudo, env, …) with
// their options are skipped, and `python -m <module>` runs the module as the program.
func commandWords(words []string) []string {
	i := 0
	for i < len(words) && strings.HasPrefix(words[i], "--") {
		i++
	}
	wrapped := false
	for i < len(words) {
		w := words[i]
		switch {
		case envAssignRe.MatchString(w), commandWrappers[w]:
			wrapped = wrapped || commandWrappers[w]
			i++
		case wrapped && strings.HasPrefix(w, "-"):
			i++
		default:
			words = words[i:]
			if len(words) >= 3 && pythonRe.MatchString(path.Base(words[0])) && words[1] == "-m" {
				return words[2:]
			}
			return words
		}
	}
	return nil
}

// ── Command-line grammars ────────────────────────────────────────────────────

// cliOption is an option that takes a value.
type cliOption struct {
	name  string         // canonical spelling, under which the value is recorded
	words *regexp.Regexp // nil: the value is the next word; else every following word it matches
}

// cliGrammar is the argument grammar of a package manager's command line. Each word after
// the program is an option, the value of an option, a redirection, or a positional word;
// the leading positional words name the subcommand and the rest are its operands. An
// option takes a value only when the grammar says so, so a value is never read as an
// operand.
type cliGrammar struct {
	program *regexp.Regexp       // the program's basename
	options map[string]cliOption // every spelling of every option that takes a value
	// singleDash: an option may be spelled with one dash or two (go's flag package).
	singleDash bool
}

// cliInvocation is one command line read against a grammar.
type cliInvocation struct {
	operands []string          // positional words after the subcommand
	values   map[string]string // last value given to each option, by canonical name
}

// parse reads cmd as an invocation of g's program running one of subcommands. ok is
// false when cmd runs another program or another subcommand.
func (g cliGrammar) parse(cmd string, subcommands ...[]string) (inv cliInvocation, ok bool) {
	words := commandWords(shellWords(cmd))
	if len(words) == 0 || !g.program.MatchString(path.Base(words[0])) {
		return inv, false
	}
	inv.values = map[string]string{}
	var positional []string
	endOfOptions := false
	for i := 1; i < len(words); i++ {
		w := words[i]
		switch {
		case endOfOptions || w == "-" || !strings.HasPrefix(w, "-"):
			if !endOfOptions && redirectRe.MatchString(w) {
				if redirectBareRe.MatchString(w) {
					i++
				}
				continue
			}
			positional = append(positional, w)
		case w == "--":
			endOfOptions = true
		default:
			name, value, attached := strings.Cut(w, "=")
			if g.singleDash && !strings.HasPrefix(name, "--") {
				name = "-" + name
			}
			opt, takesValue := g.options[name]
			switch {
			case !takesValue:
				// a flag, or a value attached to an option of no interest
			case attached:
				inv.values[opt.name] = value
			case opt.words != nil:
				for i+1 < len(words) && opt.words.MatchString(words[i+1]) {
					i++
					inv.values[opt.name] = words[i]
				}
			case i+1 < len(words):
				i++
				inv.values[opt.name] = words[i]
			}
		}
	}
	for _, sub := range subcommands {
		if len(positional) >= len(sub) && slices.Equal(positional[:len(sub)], sub) {
			inv.operands = positional[len(sub):]
			return inv, true
		}
	}
	return inv, false
}

// valueOptions builds an option table from groups of spellings; the first spelling in
// each group is the canonical name.
func valueOptions(groups ...[]string) map[string]cliOption {
	opts := map[string]cliOption{}
	for _, g := range groups {
		for _, spelling := range g {
			opts[spelling] = cliOption{name: g[0]}
		}
	}
	return opts
}

// Value-taking options of each install subcommand and the global options that may precede
// it, from each tool's own option table. testdata/cli holds those tables, and
// TestGrammars_MatchToolOptionTables holds every grammar equal to them.
var (
	// apk add --help / apk del --help (apk-tools 3)
	apkGrammar = cliGrammar{
		program: regexp.MustCompile(`^apk$`),
		options: valueOptions(
			[]string{"--virtual", "-t"},
			[]string{"--repository", "-X"},
			[]string{"--root", "-p"},
			[]string{"--arch"}, []string{"--cache-dir"}, []string{"--cache-max-age"},
			[]string{"--keys-dir"}, []string{"--preupgrade-depends"}, []string{"--progress-fd"},
			[]string{"--repositories-file"}, []string{"--repository-config"}, []string{"--timeout"},
			[]string{"--uvol-manager"}, []string{"--wait"},
		),
	}

	// apt-private/private-cmndline.cc: the options apt-get install and apt install register
	// with an argument (HasArg, ConfigFile, ArbItem)
	aptGrammar = cliGrammar{
		program: regexp.MustCompile(`^apt(?:-get)?$`),
		options: valueOptions(
			[]string{"--option", "-o"},
			[]string{"--config-file", "-c"},
			[]string{"--target-release", "-t", "--default-release"},
			[]string{"--snapshot", "-S"},
			[]string{"--with-source"}, []string{"--solver"}, []string{"--planner"}, []string{"--comment"},
		),
	}

	// pip install --help
	pipGrammar = cliGrammar{
		program: regexp.MustCompile(`^pip[0-9.]*$`),
		options: valueOptions(
			[]string{"--requirement", "-r"}, []string{"--constraint", "-c"},
			[]string{"--editable", "-e"}, []string{"--target", "-t"},
			[]string{"--config-settings", "-C"}, []string{"--index-url", "-i"},
			[]string{"--find-links", "-f"},
			[]string{"--build-constraint"}, []string{"--requirements-from-script"},
			[]string{"--platform"}, []string{"--python-version"}, []string{"--implementation"},
			[]string{"--abi"}, []string{"--root"}, []string{"--prefix"}, []string{"--src"},
			[]string{"--upgrade-strategy"}, []string{"--progress-bar"}, []string{"--root-user-action"},
			[]string{"--report"}, []string{"--group"}, []string{"--all-releases"},
			[]string{"--only-final"}, []string{"--no-binary"}, []string{"--only-binary"},
			[]string{"--extra-index-url"}, []string{"--refresh-package"},
			[]string{"--uploaded-prior-to"}, []string{"--python"}, []string{"--log"},
			[]string{"--keyring-provider"}, []string{"--proxy"}, []string{"--retries"},
			[]string{"--timeout"}, []string{"--exists-action"}, []string{"--trusted-host"},
			[]string{"--cert"}, []string{"--client-cert"}, []string{"--cache-dir"},
			[]string{"--use-feature"}, []string{"--use-deprecated"}, []string{"--resume-retries"},
		),
	}

	// ansible-galaxy collection install --help / ansible-galaxy role install --help
	galaxyGrammar = func() cliGrammar {
		opts := valueOptions(
			[]string{"--server", "-s"},
			[]string{"--token", "--api-key"},
			[]string{"--collections-path", "-p", "--roles-path"},
			[]string{"--requirements-file", "-r", "--role-file"},
			[]string{"--timeout"}, []string{"--keyring"}, []string{"--signature"},
			[]string{"--required-valid-signature-count"}, []string{"--ignore-signature-status-code"},
		)
		// nargs="+": consumes every following word that is a GnuPG status code.
		opts["--ignore-signature-status-codes"] = cliOption{
			name:  "--ignore-signature-status-codes",
			words: regexp.MustCompile(`^(?:EXPSIG|EXPKEYSIG|REVKEYSIG|BADSIG|ERRSIG|NO_PUBKEY|MISSING_PASSPHRASE|BAD_PASSPHRASE|NODATA|UNEXPECTED|ERROR|FAILURE|BADARMOR|KEYEXPIRED|KEYREVOKED|NO_SECKEY)$`),
		}
		return cliGrammar{program: regexp.MustCompile(`^ansible-galaxy$`), options: opts}
	}()

	// npm 11 config definitions: every key whose type does not admit Boolean takes a value
	// (an unknown option is boolean to npm's parser).
	npmGrammar = cliGrammar{
		program: regexp.MustCompile(`^npm$`),
		options: valueOptions(
			[]string{"--prefix", "-C"}, []string{"--workspace", "-w"},
			[]string{"--location", "-L"}, []string{"--call", "-c"}, []string{"--message", "-m"},
			[]string{"--_auth"}, []string{"--access"}, []string{"--allow-directory"}, []string{"--allow-file"},
			[]string{"--allow-git"}, []string{"--allow-remote"}, []string{"--allow-scripts"},
			[]string{"--also"}, []string{"--audit-level"}, []string{"--auth-type"}, []string{"--before"},
			[]string{"--ca"}, []string{"--cache"}, []string{"--cache-max"}, []string{"--cache-min"},
			[]string{"--cafile"}, []string{"--cert"}, []string{"--cidr"}, []string{"--cpu"},
			[]string{"--depth"}, []string{"--diff"}, []string{"--diff-dst-prefix"},
			[]string{"--diff-src-prefix"}, []string{"--diff-unified"}, []string{"--editor"},
			[]string{"--expect-result-count"}, []string{"--expires"}, []string{"--fetch-retries"},
			[]string{"--fetch-retry-factor"}, []string{"--fetch-retry-maxtimeout"},
			[]string{"--fetch-retry-mintimeout"}, []string{"--fetch-timeout"}, []string{"--git"},
			[]string{"--globalconfig"}, []string{"--heading"}, []string{"--https-proxy"},
			[]string{"--include"}, []string{"--init-author-email"}, []string{"--init-author-name"},
			[]string{"--init-author-url"}, []string{"--init-license"}, []string{"--init-module"},
			[]string{"--init-type"}, []string{"--init-version"}, []string{"--init.author.email"},
			[]string{"--init.author.name"}, []string{"--init.author.url"}, []string{"--init.license"},
			[]string{"--init.module"}, []string{"--init.version"}, []string{"--install-strategy"},
			[]string{"--key"}, []string{"--libc"}, []string{"--local-address"},
			[]string{"--lockfile-version"}, []string{"--loglevel"}, []string{"--logs-dir"},
			[]string{"--logs-max"}, []string{"--maxsockets"}, []string{"--min-release-age"},
			[]string{"--min-release-age-exclude"}, []string{"--name"}, []string{"--node-gyp"},
			[]string{"--node-options"}, []string{"--noproxy"}, []string{"--omit"}, []string{"--only"},
			[]string{"--orgs"}, []string{"--os"}, []string{"--otp"}, []string{"--package"},
			[]string{"--pack-destination"}, []string{"--packages"}, []string{"--preid"},
			[]string{"--provenance-file"}, []string{"--proxy"}, []string{"--registry"},
			[]string{"--replace-registry-host"}, []string{"--save-prefix"}, []string{"--sbom-format"},
			[]string{"--sbom-type"}, []string{"--scope"}, []string{"--scopes"},
			[]string{"--packages-and-scopes-permission"}, []string{"--orgs-permission"},
			[]string{"--password"}, []string{"--token-description"}, []string{"--script-shell"},
			[]string{"--searchexclude"}, []string{"--searchlimit"}, []string{"--searchopts"},
			[]string{"--searchstaleness"}, []string{"--shell"}, []string{"--tag"},
			[]string{"--tag-version-prefix"}, []string{"--umask"}, []string{"--user-agent"},
			[]string{"--userconfig"}, []string{"--viewer"}, []string{"--which"},
		),
	}

	// go help build (build flags shared by go install)
	goGrammar = cliGrammar{
		program:    regexp.MustCompile(`^go$`),
		singleDash: true,
		options: valueOptions(
			[]string{"--C"}, []string{"--p"}, []string{"--covermode"}, []string{"--coverpkg"},
			[]string{"--asmflags"}, []string{"--buildmode"}, []string{"--compiler"},
			[]string{"--gccgoflags"}, []string{"--gcflags"}, []string{"--installsuffix"},
			[]string{"--ldflags"}, []string{"--mod"}, []string{"--modfile"}, []string{"--overlay"},
			[]string{"--pgo"}, []string{"--pkgdir"}, []string{"--tags"}, []string{"--toolexec"},
		),
	}
)
