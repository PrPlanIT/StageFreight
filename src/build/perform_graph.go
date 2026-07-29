package build

import (
	"fmt"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// scribeNodePrefix marks a scribe-item node in the unified perform graph. It reuses
// the same typed prefix as depends_on, so a build id and a scribe id can be equal
// without colliding — the node id space is disjoint by construction.
const scribeNodePrefix = "scribe:"

// PerformNode is one node in the perform execution order: either a build to run or a
// build-fed scribe item to render. Exactly one of Build / ScribeID is set.
type PerformNode struct {
	Build    *config.BuildConfig // non-nil → run this build
	ScribeID string              // non-empty → render this scribe content item
}

// IsScribe reports whether this node is a scribe render (vs a build).
func (n PerformNode) IsScribe() bool { return n.ScribeID != "" }

// PerformOrder topologically orders the builds together with the scribe items that
// builds CONSUME (build-fed), so each consumed item renders AFTER its upstream build
// (contents/include `build:`) and BEFORE the build that bakes it (`depends_on:
// scribe:<id>`). Scribe items no build consumes are omitted here — they render late,
// in publish. Deterministic; errors on a cycle or a dep on an absent build.
//
// This is the "render → consumer ordering" of #39: the graph decides when a scribe
// item renders (before its consumer), and late items fall through to publish.
func PerformOrder(builds []config.BuildConfig, scribe config.ScribeConfig) ([]PerformNode, error) {
	byBuildID := make(map[string]*config.BuildConfig, len(builds))
	for i := range builds {
		byBuildID[builds[i].ID] = &builds[i]
	}
	scribeByID := scribe.ContentByID()

	// The build-fed set: scribe items named by some build's depends_on. Only these
	// enter the perform graph; everything else is a late (publish) item.
	fed := map[string]bool{}
	for _, b := range builds {
		for _, s := range b.ScribeDeps() {
			fed[s] = true
		}
	}

	indeg := map[string]int{}
	children := map[string][]string{}
	addNode := func(id string) {
		if _, ok := indeg[id]; !ok {
			indeg[id] = 0
		}
	}
	addEdge := func(parent, child string) {
		children[parent] = append(children[parent], child)
		indeg[child]++
	}

	// Build nodes + their outbound edges (build→build, stage, build→scribe).
	for _, b := range builds {
		addNode(b.ID)
		bd := b.BuildDeps()
		for _, dep := range bd {
			addNode(dep)
			addEdge(dep, b.ID)
		}
		if b.Stage != nil && b.Stage.From != "" && !containsStr(bd, b.Stage.From) {
			addNode(b.Stage.From)
			addEdge(b.Stage.From, b.ID)
		}
		for _, s := range b.ScribeDeps() {
			addNode(scribeNodePrefix + s)
			addEdge(scribeNodePrefix+s, b.ID)
		}
	}

	// Build-fed scribe nodes + their upstream-build edge (contents/include `build:`).
	for sid := range fed {
		node := scribeNodePrefix + sid
		addNode(node)
		if c, ok := scribeByID[sid]; ok && c.Build != "" {
			addNode(c.Build)
			addEdge(c.Build, node)
		}
	}

	// Kahn's, seeded and processed in sorted order for determinism.
	var queue []string
	for _, id := range sortedKeys(indeg) {
		if indeg[id] == 0 {
			queue = append(queue, id)
		}
	}
	var orderedIDs []string
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		orderedIDs = append(orderedIDs, id)
		next := append([]string(nil), children[id]...)
		sort.Strings(next)
		for _, c := range next {
			indeg[c]--
			if indeg[c] == 0 {
				queue = append(queue, c)
			}
		}
	}
	if len(orderedIDs) != len(indeg) {
		var cyc []string
		for id, d := range indeg {
			if d > 0 {
				cyc = append(cyc, id)
			}
		}
		sort.Strings(cyc)
		return nil, fmt.Errorf("dependency cycle among builds/scribe: %s", strings.Join(cyc, ", "))
	}

	out := make([]PerformNode, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		if sid, ok := strings.CutPrefix(id, scribeNodePrefix); ok {
			out = append(out, PerformNode{ScribeID: sid})
			continue
		}
		b, ok := byBuildID[id]
		if !ok {
			return nil, fmt.Errorf("depends_on references unknown build %q", id)
		}
		out = append(out, PerformNode{Build: b})
	}
	return out, nil
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
