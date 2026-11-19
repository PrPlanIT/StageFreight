package disk

import (
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// tempSuffix is the random part os.MkdirTemp / os.CreateTemp put in a name.
var tempSuffix = regexp.MustCompile(`^(.*?)\d{4,}((?:\.[A-Za-z0-9]+)*)$`)

// ScanTemp builds the TEMP domain for a StageFreight temp root: its entries grouped by
// name, those untouched for a day reclaimable. Nil when it holds nothing.
func ScanTemp(root string) *Node {
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) == 0 {
		return nil
	}
	dom := &Node{Label: "TEMP " + filepath.Base(root), Path: root, Attr: Attribution{Runtime: "temp"}}
	groups := map[string]*Node{}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		name := e.Name()
		if m := tempSuffix.FindStringSubmatch(name); m != nil {
			name = m[1] + "*" + m[2]
		}
		g := groups[name]
		if g == nil {
			g = &Node{Label: name, Attr: Attribution{Runtime: "temp"}, Flags: FlagReclaimable,
				Hint: &Hint{Command: "stagefreight prune --confirm", Safety: "safe"}}
			groups[name] = g
			dom.add(g)
		}
		size := info.Size()
		if e.IsDir() {
			size = dirSize(filepath.Join(root, e.Name()))
		}
		g.Bytes += size
		if info.ModTime().After(cutoff) {
			g.Flags &^= FlagReclaimable // a run may still hold it
			g.Hint = nil
		}
	}
	for _, c := range dom.Kids {
		dom.Bytes += c.Bytes
	}
	dom.sortKids()
	return dom
}
