package docsgen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/cistate"
)

// TestDocsFreshness_FactVocabulary is the facts ratchet: every templateable
// fact in the cistate vocabulary registry must appear in the Narration &
// Notifications reference. A new fact ships WITH its documentation or this
// fails the build.
func TestDocsFreshness_FactVocabulary(t *testing.T) {
	doc := readRepoFile(t, "docs/config/narration.md")

	for _, name := range cistate.BareFacts {
		if !strings.Contains(doc, "{"+name+"}") {
			t.Errorf("bare fact {%s} is undocumented in docs/config/narration.md", name)
		}
	}
	for domain, keys := range cistate.DomainFacts {
		if !strings.Contains(doc, fmt.Sprintf("`%s.`", domain)) {
			t.Errorf("fact domain %q is undocumented in docs/config/narration.md", domain)
			continue
		}
		for _, key := range keys {
			if !strings.Contains(doc, "`"+key+"`") && !strings.Contains(doc, "{"+domain+"."+key+"}") {
				t.Errorf("fact {%s.%s} is undocumented in docs/config/narration.md", domain, key)
			}
		}
	}
}
