package dependency

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// applyUpstreamUpdates moves a declared upstream's pins (upstream.sources.<id>.pins). The
// pins of one source are one coherent set: every pin is verified to still hold its expected
// value before any file is written, and either all of them move or none do. Each dependency
// carries the exact line and byte column (Group names its source) of its value, so the edit replaces that value only
// and preserves everything else on the line.
func applyUpstreamUpdates(deps []supplychain.Dependency, repoRoot string) ([]AppliedUpdate, []SkippedDep, []string, error) {
	var applied []AppliedUpdate
	var skipped []SkippedDep
	var touched []string

	bySource := map[string][]supplychain.Dependency{}
	var order []string
	for _, d := range deps {
		if _, seen := bySource[d.Group]; !seen {
			order = append(order, d.Group)
		}
		bySource[d.Group] = append(bySource[d.Group], d)
	}

	for _, src := range order {
		group := bySource[src]
		files := map[string][]string{}
		reason := ""
		for _, d := range group {
			lines, ok := files[d.File]
			if !ok {
				data, err := os.ReadFile(filepath.Join(repoRoot, d.File))
				if err != nil {
					reason = fmt.Sprintf("%s: %v", d.File, err)
					break
				}
				lines = strings.Split(string(data), "\n")
				files[d.File] = lines
			}
			if d.Line < 1 || d.Line > len(lines) {
				reason = fmt.Sprintf("%s:%d no longer exists", d.File, d.Line)
				break
			}
			l := lines[d.Line-1]
			if d.Column+len(d.Current) > len(l) || l[d.Column:d.Column+len(d.Current)] != d.Current {
				reason = fmt.Sprintf("%s:%d no longer holds %s", d.File, d.Line, d.Current)
				break
			}
		}
		if reason != "" {
			for _, d := range group {
				skipped = append(skipped, SkippedDep{Dep: d, Category: SkipSourceMismatch, Reason: "upstream " + src + " pins not moved: " + reason})
			}
			continue
		}

		// Apply right-to-left within a line so earlier columns stay valid.
		sort.SliceStable(group, func(i, j int) bool {
			a, b := group[i], group[j]
			if a.File != b.File {
				return a.File < b.File
			}
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			return a.Column > b.Column
		})
		for _, d := range group {
			lines := files[d.File]
			l := lines[d.Line-1]
			target := d.UpdateTarget()
			lines[d.Line-1] = l[:d.Column] + target + l[d.Column+len(d.Current):]
			applied = append(applied, AppliedUpdate{Dep: d, OldVer: d.Current, NewVer: target, UpdateType: updateType(d.Current, target)})
		}
		for file, lines := range files {
			abs := filepath.Join(repoRoot, file)
			info, err := os.Stat(abs)
			if err != nil {
				return applied, skipped, touched, err
			}
			if err := os.WriteFile(abs, []byte(strings.Join(lines, "\n")), info.Mode().Perm()); err != nil {
				return applied, skipped, touched, fmt.Errorf("writing %s: %w", file, err)
			}
			touched = append(touched, file)
		}
	}
	sort.Strings(touched)
	return applied, skipped, touched, nil
}
