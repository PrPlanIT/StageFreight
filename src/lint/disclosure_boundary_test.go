package lint

import (
	"reflect"
	"testing"
)

// The disclosure/finding separation is a TYPE boundary, not a convention: a disclosure
// must never be able to grade (Severity) or mutate (Fix). The disclosure types are
// intentionally distinct from Finding and must stay free of those fields. This test is
// the anti-regression LOCK — it fails the moment a future change adds either field to a
// disclosure type, so the distinction cannot be casually collapsed.
func TestDisclosureTypesCannotGradeOrMutate(t *testing.T) {
	forbidden := []string{"Severity", "Fix"}
	for _, typ := range []reflect.Type{
		reflect.TypeOf(NonTextEntry{}),
		reflect.TypeOf(ProvenanceEntry{}),
	} {
		for _, f := range forbidden {
			if _, ok := typ.FieldByName(f); ok {
				t.Errorf("%s must not have a %q field — disclosures never grade or mutate", typ.Name(), f)
			}
		}
	}
}
