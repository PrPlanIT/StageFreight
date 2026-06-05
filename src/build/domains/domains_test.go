package domains

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// ── mock contributors ────────────────────────────────────────────────────────

type mockBase struct {
	name    string
	order   int
	applies bool
}

func (m mockBase) Name() string                { return m.name }
func (m mockBase) Order() int                  { return m.order }
func (m mockBase) Applies(rc *RunContext) bool { return m.applies }

// detectOnly participates in Detect only — used to prove callDomain returns
// participates=false for domains a contributor does not implement.
type detectOnly struct{ mockBase }

func (d detectOnly) Detect(rc *RunContext) (Contribution, error) {
	return Contribution{Rows: []string{"row"}, Status: "success", Summary: "ok"}, nil
}

// fullContrib participates in every domain plus the optional interfaces.
type fullContrib struct {
	mockBase
	docker, crucible bool
	concluded        *bool
}

func (f fullContrib) Detect(rc *RunContext) (Contribution, error) {
	return Contribution{Status: "success"}, nil
}
func (f fullContrib) Plan(rc *RunContext) (Contribution, error) {
	return Contribution{Status: "success"}, nil
}
func (f fullContrib) Build(rc *RunContext) (Contribution, error) {
	return Contribution{Status: "success"}, nil
}
func (f fullContrib) Verify(rc *RunContext) (Contribution, error) {
	return Contribution{Status: "success"}, nil
}
func (f fullContrib) Publish(rc *RunContext) (Contribution, error) {
	return Contribution{Status: "success"}, nil
}
func (f fullContrib) NeedsDocker() bool       { return f.docker }
func (f fullContrib) NeedsCrucible() bool     { return f.crucible }
func (f fullContrib) Conclude(rc *RunContext) { *f.concluded = true }

func withRegistry(t *testing.T, factories ...func() Contributor) {
	t.Helper()
	saved := registry
	t.Cleanup(func() { registry = saved })
	registry = nil
	for _, f := range factories {
		RegisterContributor(f)
	}
}

func names(cs []Contributor) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name()
	}
	return out
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestApplicableGatesAndSortsByOrder(t *testing.T) {
	// Registration order is intentionally NOT the Order order, to prove sorting.
	withRegistry(t,
		func() Contributor { return detectOnly{mockBase{"docker", 20, true}} },
		func() Contributor { return detectOnly{mockBase{"skipme", 5, false}} }, // Applies=false → excluded
		func() Contributor { return detectOnly{mockBase{"binary", 10, true}} },
	)

	active := applicable(&RunContext{Config: &config.Config{}})

	if got := names(active); len(got) != 2 || got[0] != "binary" || got[1] != "docker" {
		t.Fatalf("want [binary docker] (Applies-gated, Order-sorted), got %v", got)
	}
}

func TestApplicableRespectsOnly(t *testing.T) {
	withRegistry(t,
		func() Contributor { return detectOnly{mockBase{"binary", 10, true}} },
		func() Contributor { return detectOnly{mockBase{"docker", 20, true}} },
	)

	active := applicable(&RunContext{Config: &config.Config{}, Only: []string{"docker"}})

	if got := names(active); len(got) != 1 || got[0] != "docker" {
		t.Fatalf("Only=[docker]: want [docker], got %v", got)
	}
}

func TestCallDomainDispatch(t *testing.T) {
	rc := &RunContext{}
	c := detectOnly{mockBase{"docker", 20, true}}

	if _, ok, err := callDomain(rc, DomainDetect, c); !ok || err != nil {
		t.Fatalf("Detect: want participate, got ok=%v err=%v", ok, err)
	}
	// detectOnly implements no Builder → must NOT participate in Build.
	if _, ok, _ := callDomain(rc, DomainBuild, c); ok {
		t.Fatalf("Build: detectOnly must not participate")
	}
}

func TestSubstrateNeedsOptional(t *testing.T) {
	// A plain detectOnly does not implement SubstrateNeeds.
	if _, ok := interface{}(detectOnly{}).(SubstrateNeeds); ok {
		t.Fatal("detectOnly should not satisfy SubstrateNeeds")
	}
	// fullContrib does.
	if _, ok := interface{}(fullContrib{}).(SubstrateNeeds); !ok {
		t.Fatal("fullContrib should satisfy SubstrateNeeds")
	}
}

func TestConcludeAllCallsConcluders(t *testing.T) {
	concluded := false
	active := []Contributor{
		detectOnly{mockBase{"binary", 10, true}}, // no Conclude
		fullContrib{mockBase: mockBase{"docker", 20, true}, concluded: &concluded},
	}
	concludeAll(&RunContext{}, active)
	if !concluded {
		t.Fatal("concludeAll did not invoke the Concluder")
	}
}
