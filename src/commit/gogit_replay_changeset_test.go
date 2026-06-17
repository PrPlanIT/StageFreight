package commit

import (
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// TestEqualChangeSets verifies the change-set equivalence gate enforces full
// object-layer identity — operation, path, blob OID, and file mode — not merely
// the path-set. This is what makes diff(base, replayed) == diff(base, source)
// sufficient to prove resulting tree identity.
func TestEqualChangeSets(t *testing.T) {
	h1 := plumbing.NewHash("1111111111111111111111111111111111111111")
	h2 := plumbing.NewHash("2222222222222222222222222222222222222222")
	add := func(name string, h plumbing.Hash, mode filemode.FileMode) *object.Change {
		return &object.Change{To: object.ChangeEntry{
			Name: name, Tree: &object.Tree{},
			TreeEntry: object.TreeEntry{Name: filepath.Base(name), Mode: mode, Hash: h},
		}}
	}
	base := object.Changes{
		add("src/gitops/validate.go", h1, filemode.Regular),
		add("hack/build.sh", h2, filemode.Executable),
	}

	cases := []struct {
		name  string
		other object.Changes
		equal bool
	}{
		{"identical reordered", object.Changes{base[1], base[0]}, true},
		{"blob OID differs at same path", object.Changes{add("src/gitops/validate.go", h2, filemode.Regular), base[1]}, false},
		{"file mode differs (exec bit)", object.Changes{add("src/gitops/validate.go", h1, filemode.Executable), base[1]}, false},
		{"basename-root remap (the incident)", object.Changes{add("validate.go", h1, filemode.Regular), base[1]}, false},
		{"extra path", object.Changes{base[0], base[1], add("src/extra.go", h1, filemode.Regular)}, false},
		{"missing path", object.Changes{base[0]}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := equalChangeSets(base, tc.other); got != tc.equal {
				t.Errorf("equalChangeSets = %v, want %v", got, tc.equal)
			}
		})
	}
}
