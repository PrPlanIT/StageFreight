package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
)

// forkForge is a forge that answers ForkParent.
type forkForge struct {
	forge.Forge
	parent string
	err    error
	calls  *int
}

func (f forkForge) Provider() forge.Provider { return forge.GitHub }
func (f forkForge) ForkParent(context.Context) (string, error) {
	*f.calls++
	return f.parent, f.err
}

// plainForge cannot report a fork parent.
type plainForge struct{ forge.Forge }

func (plainForge) Provider() forge.Provider { return forge.GitHub }

func TestResolveForkParents(t *testing.T) {
	calls := 0
	clients := map[string]forge.Forge{
		"fork":    forkForge{parent: "https://github.com/up/r.git", calls: &calls},
		"notfork": forkForge{calls: &calls},
		"broken":  forkForge{err: errors.New("403"), calls: &calls},
		"plain":   plainForge{},
	}
	orig := forkParentClient
	defer func() { forkParentClient = orig }()
	forkParentClient = func(_ *config.Config, id string) (forge.Forge, error) { return clients[id], nil }

	cfg := &config.Config{Upstream: config.UpstreamConfig{Sources: config.OrderedUpstreamSources{
		{ID: "a", ForkParent: "fork"},
		{ID: "b", ForkParent: "notfork"},
		{ID: "c", ForkParent: "broken"},
		{ID: "d", ForkParent: "plain"},
		{ID: "e", Git: "https://github.com/x/y"},
	}}}
	srcs, err := upstreamSources(context.Background(), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if srcs[0].Location != "https://github.com/up/r.git" || srcs[0].Unresolved != "" {
		t.Errorf("a fork reads its parent: %+v", srcs[0])
	}
	for i, want := range map[int]string{1: "not a fork", 2: "403", 3: "cannot report"} {
		if !strings.Contains(srcs[i].Unresolved, want) || srcs[i].Location != "" {
			t.Errorf("%s: fails closed with %q, got %+v", srcs[i].ID, want, srcs[i])
		}
	}
	if srcs[4].Location != "https://github.com/x/y" {
		t.Errorf("a written git source is untouched: %+v", srcs[4])
	}
	before := calls
	if _, err := upstreamSources(context.Background(), cfg, ""); err != nil || calls != before {
		t.Errorf("each parent is looked up once per run (%d more calls)", calls-before)
	}
}
