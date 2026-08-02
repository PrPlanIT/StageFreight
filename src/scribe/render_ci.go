package scribe

import (
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// defaultCIRowLimit is the self-bounding cap for ci producer rows — small enough
// for a phone notification, overridable per stencil with limit:.
const defaultCIRowLimit = 6

// renderCIStencil renders a type: ci producer — one of the four looping views of
// the run's recorded state (failures / vulns / artifacts / changelog). Rows come
// from cistate (the subsystem Results row-lists and the lifecycle records); the
// producer self-bounds at limit (excess folds into a "+K more" tail) and renders
// "" when there is nothing — which, with line elision, is what makes it safe to
// embed anywhere.
func renderCIStencil(def config.StencilDef, st *cistate.State) string {
	section := def.Section
	if section == "" {
		section = def.ID
	}
	var rows []string
	switch section {
	case "failures":
		rows = failureRows(st)
	case "vulns":
		rows = prefixRows("⚠ ", resultRows(st, "security", "blocking_list"))
	case "artifacts":
		rows = prefixRows("+ ", resultRows(st, "build", "artifacts"))
	case "changelog":
		rows = prefixRows("⚠ BREAKING · ", resultRows(st, "changelog", "breaking"))
	}
	limit := def.Limit
	if limit <= 0 {
		limit = defaultCIRowLimit
	}
	return boundRows(rows, limit)
}

// failureRows lists what broke: each failing test by name, then any subsystem's
// own recorded failure rows (Results["failures"], newline-joined — reconcile's
// per-kustomization failures, and whatever future domains record), then a
// generic "✗ name — reason" for failed subsystems that provided no rows of
// their own. The test subsystem never gets a generic row — it is the headline
// fact ({failure.subsystem}), and its per-test rows already speak.
func failureRows(st *cistate.State) []string {
	var rows []string
	covered := map[string]bool{"test": true}
	if tests := st.GetSubsystem("test"); tests != nil && tests.Results["failures"] != "" {
		for _, name := range strings.Split(tests.Results["failures"], ", ") {
			if name != "" {
				rows = append(rows, "✗ "+name)
			}
		}
	}
	for i := range st.Subsystems {
		s := &st.Subsystems[i]
		if s.Name == "test" || s.Results["failures"] == "" {
			continue
		}
		for _, r := range strings.Split(s.Results["failures"], "\n") {
			if r = strings.TrimSpace(r); r != "" {
				rows = append(rows, "✗ "+r)
			}
		}
		covered[s.Name] = true
	}
	for i := range st.Subsystems {
		s := &st.Subsystems[i]
		if !s.Attempted || s.Outcome != "failed" || covered[s.Name] {
			continue
		}
		if s.Reason != "" {
			rows = append(rows, fmt.Sprintf("✗ %s — %s", s.Name, s.Reason))
		} else {
			rows = append(rows, "✗ "+s.Name+" failed")
		}
	}
	return rows
}

// resultRows splits a newline-joined row-list fact recorded in a subsystem's
// Results (the convention for structured rows in the flat fact map).
func resultRows(st *cistate.State, subsystem, key string) []string {
	sub := st.GetSubsystem(subsystem)
	if sub == nil || sub.Results[key] == "" {
		return nil
	}
	var rows []string
	for _, r := range strings.Split(sub.Results[key], "\n") {
		if r = strings.TrimSpace(r); r != "" {
			rows = append(rows, r)
		}
	}
	return rows
}

func prefixRows(prefix string, rows []string) []string {
	for i := range rows {
		rows[i] = prefix + rows[i]
	}
	return rows
}

// boundRows joins rows capped at limit, folding the excess into a "+K more" tail.
func boundRows(rows []string, limit int) string {
	if len(rows) == 0 {
		return ""
	}
	if len(rows) > limit {
		excess := len(rows) - limit
		rows = append(rows[:limit:limit], fmt.Sprintf("+%d more", excess))
	}
	return strings.Join(rows, "\n")
}
