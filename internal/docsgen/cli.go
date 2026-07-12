package docsgen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/PrPlanIT/StageFreight/src/cli/cliflag"
)

// commandsToSkip are cobra-generated boilerplate subtrees with no StageFreight-specific
// content (shell completion scripts, the generic help command). Omitting them keeps the
// reference focused on the tool's real surface — no information about StageFreight is lost.
var commandsToSkip = map[string]bool{
	"completion": true,
	"help":       true,
}

// GenerateCLIReference walks the Cobra command tree and emits a CLI reference document:
// a grouped command tree, a single Global flags section, then one section per command.
// Hidden and boilerplate commands are omitted.
func GenerateCLIReference(root *cobra.Command) string {
	var b strings.Builder
	b.WriteString(generatedHeader)

	cmds := collectCommands(root)
	rootDepth := strings.Count(fullPath(root), " ")

	// Command tree — indented by depth so subcommands nest visually under their parent,
	// rather than a flat alphabetical dump.
	b.WriteString("## Command index\n\n")
	for _, c := range cmds {
		depth := strings.Count(fullPath(c), " ") - rootDepth
		label := c.Name()
		if depth == 0 {
			label = fullPath(c)
		}
		short := c.Short
		if short == "" {
			short = "—"
		}
		b.WriteString(fmt.Sprintf("%s- [`%s`](#%s) — %s\n",
			strings.Repeat("    ", depth), label, anchor("cli", fullPath(c)), short))
	}
	b.WriteString("\n")

	// Global flags — documented once; every command references this instead of repeating it.
	b.WriteString(renderGlobalFlags(root))
	b.WriteString("---\n\n")

	for _, c := range cmds {
		b.WriteString(renderCommand(c))
	}

	return b.String()
}

// collectCommands returns all documentable commands (depth-first), skipping hidden and
// boilerplate subtrees.
func collectCommands(root *cobra.Command) []*cobra.Command {
	var result []*cobra.Command
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Hidden || commandsToSkip[cmd.Name()] {
			return
		}
		result = append(result, cmd)
		for _, sub := range sortedChildren(cmd) {
			walk(sub)
		}
	}
	walk(root)
	return result
}

func fullPath(cmd *cobra.Command) string { return cmd.CommandPath() }

// renderGlobalFlags documents the root persistent flags once, under a stable anchor.
func renderGlobalFlags(root *cobra.Command) string {
	rows := flagRowsFrom(root.PersistentFlags())
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(anchorTag("cli", "global-flags") + "\n")
	b.WriteString("## Global flags\n\n")
	b.WriteString("Available on every command:\n\n")
	b.WriteString(flagTable(rows))
	b.WriteString("\n")
	return b.String()
}

func renderCommand(cmd *cobra.Command) string {
	var b strings.Builder
	path := fullPath(cmd)

	b.WriteString(anchorTag("cli", path) + "\n")
	b.WriteString(fmt.Sprintf("### %s\n\n", path))

	// A single parent link for navigation — replaces the old footer that dumped every
	// sibling and top-level command after every section.
	if parent := cmd.Parent(); parent != nil {
		b.WriteString(fmt.Sprintf("*↩ [`%s`](#%s)*\n\n", fullPath(parent), anchor("cli", fullPath(parent))))
	}

	if cmd.Deprecated != "" {
		b.WriteString(fmt.Sprintf("!!! warning \"Deprecated\"\n    %s\n\n", cmd.Deprecated))
	}

	// Usage: the full command path plus only the argument portion of Use (Use's first
	// token is the command name itself — dropping it avoids "stagefreight badge badge").
	usage := path
	if args := strings.TrimSpace(strings.TrimPrefix(cmd.Use, cmd.Name())); args != "" {
		usage += " " + args
	}
	b.WriteString(fmt.Sprintf("**Usage:** `%s`\n\n", usage))

	if len(cmd.Aliases) > 0 {
		b.WriteString(fmt.Sprintf("**Aliases:** %s\n\n", strings.Join(cmd.Aliases, ", ")))
	}

	desc := cmd.Long
	if desc == "" {
		desc = cmd.Short
	}
	if desc != "" {
		b.WriteString(formatDescription(desc) + "\n\n")
	}

	// Examples in a collapsible block — the full example text is kept, just folded so a
	// command with a dozen examples doesn't dominate the page.
	if cmd.Example != "" {
		b.WriteString("??? example \"Examples\"\n\n")
		for _, line := range append([]string{"```"}, append(strings.Split(strings.TrimRight(cmd.Example, "\n"), "\n"), "```")...) {
			b.WriteString("    " + line + "\n")
		}
		b.WriteString("\n")
	}

	localRows := localFlagRows(cmd)
	if len(localRows) > 0 {
		b.WriteString("**Flags:**\n\n")
		b.WriteString(flagTable(localRows))
		b.WriteString("\n")
	}
	// The global-flags pointer is only useful where flags actually apply: a runnable
	// command or one with its own flags. A pure group (just subcommands) skips it.
	if hasInheritedFlags(cmd) && (len(localRows) > 0 || cmd.Runnable()) {
		b.WriteString(fmt.Sprintf("_Plus the [global flags](#%s)._\n\n", anchor("cli", "global-flags")))
	}

	if subs := sortedChildren(cmd); len(subs) > 0 {
		b.WriteString("**Subcommands:**\n\n")
		for _, sub := range subs {
			short := sub.Short
			if short == "" {
				short = "—"
			}
			b.WriteString(fmt.Sprintf("- [`%s`](#%s) — %s\n", sub.Name(), anchor("cli", fullPath(sub)), short))
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n\n")
	return b.String()
}

// formatDescription renders a command's Long text for markdown. Many commands embed
// example blocks in Long as 2-space-indented lines, which markdown collapses into a
// run-on paragraph. This wraps any run of indented lines in a fenced code block so those
// examples render as code — fixing every command's examples without touching cobra source.
func formatDescription(desc string) string {
	var out []string
	inCode := false
	closeFence := func() {
		if inCode {
			out = append(out, "```")
			inCode = false
		}
	}
	for _, ln := range strings.Split(strings.TrimRight(desc, "\n"), "\n") {
		trimmed := strings.TrimLeft(ln, " \t")
		switch {
		case trimmed == "": // blank line: end any code block, keep the blank
			closeFence()
			out = append(out, "")
		case ln != trimmed: // indented → code
			if !inCode {
				out = append(out, "```")
				inCode = true
			}
			out = append(out, trimmed)
		default: // ordinary prose
			closeFence()
			out = append(out, ln)
		}
	}
	closeFence()
	return strings.Join(out, "\n")
}

// sortedChildren returns a command's documentable subcommands, name-sorted.
func sortedChildren(cmd *cobra.Command) []*cobra.Command {
	var subs []*cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Hidden || commandsToSkip[sub.Name()] {
			continue
		}
		subs = append(subs, sub)
	}
	sort.Slice(subs, func(i, j int) bool { return subs[i].Name() < subs[j].Name() })
	return subs
}

// localFlagRows returns a command's own flags (excluding inherited/global ones).
func localFlagRows(cmd *cobra.Command) []flagRow {
	inherited := map[string]bool{}
	if inh := cmd.InheritedFlags(); inh != nil {
		inh.VisitAll(func(f *pflag.Flag) { inherited[f.Name] = true })
	}
	var rows []flagRow
	if lf := cmd.LocalFlags(); lf != nil {
		lf.VisitAll(func(f *pflag.Flag) {
			if f.Hidden || inherited[f.Name] {
				return
			}
			rows = append(rows, flagRowFrom(f))
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func flagRowsFrom(fs *pflag.FlagSet) []flagRow {
	var rows []flagRow
	if fs != nil {
		fs.VisitAll(func(f *pflag.Flag) {
			if f.Hidden {
				return
			}
			rows = append(rows, flagRowFrom(f))
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func hasInheritedFlags(cmd *cobra.Command) bool {
	found := false
	if inh := cmd.InheritedFlags(); inh != nil {
		inh.VisitAll(func(f *pflag.Flag) {
			if !f.Hidden {
				found = true
			}
		})
	}
	return found
}

func flagRowFrom(f *pflag.Flag) flagRow {
	name := "--" + f.Name
	if f.Shorthand != "" {
		name = "-" + f.Shorthand + ", " + name
	}
	usage := f.Usage
	var options []string
	// Self-describing enum flags carry their allowed values — surface them in the Options
	// column and strip the "(one of: …)" hint from the description to avoid duplicating it.
	if ev, ok := f.Value.(*cliflag.EnumValue); ok {
		options = ev.Allowed()
		usage = strings.TrimSuffix(usage, cliflag.OptionsSuffix(options))
	}
	return flagRow{
		Name:        name,
		Type:        f.Value.Type(),
		Default:     formatDefault(f),
		Description: sanitizeCell(usage),
		Options:     options,
	}
}

// sanitizeCell flattens a multi-line flag usage into a single table cell: continuation
// lines become <br> (kept, not lost) and pipes are escaped so they don't break the table.
func sanitizeCell(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			kept = append(kept, t)
		}
	}
	return strings.ReplaceAll(strings.Join(kept, "<br>"), "|", "\\|")
}

func formatDefault(f *pflag.Flag) string {
	if f.DefValue == "" || f.DefValue == "false" || f.DefValue == "0" || f.DefValue == "[]" {
		return "—"
	}
	return "`" + f.DefValue + "`"
}
