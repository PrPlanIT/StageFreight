package toolchain

import "testing"

// Every registry tool needs a built-in version, so StageFreight's own config wants each one
// and its dependency update locks it.
func TestBuiltinLock_CoversRegistry(t *testing.T) {
	l, err := builtinLock()
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range AllTools() {
		if BuiltinVersion(def.Name) == "" {
			t.Errorf("%s has no built-in version: add it to toolchains.want in .stagefreight.yml and run the dependency update", def.Name)
		}
	}
	for _, e := range l.Toolchains {
		if _, ok := LookupTool(e.Name); !ok {
			t.Errorf("the embedded lock names %s, which is no registry tool", e.Name)
		}
	}
}
