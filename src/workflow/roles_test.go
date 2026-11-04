package workflow

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
)

func TestEffective(t *testing.T) {
	cases := []struct {
		ceiling   Role
		known     bool
		requested string
		want      Role
		err       string
	}{
		{Producer, true, "", Talent, ""},           // everyone acts as talent by default
		{Producer, true, "director", Director, ""}, // raising within the ceiling is deliberate
		{Producer, true, "audience", Audience, ""},
		{Talent, true, "producer", 0, "can only lower"},
		{Audience, true, "", Audience, ""},          // the default never exceeds the ceiling
		{Audience, false, "producer", Producer, ""}, // unknown ceiling: unbounded
		{Producer, true, "boss", 0, "unknown role"},
	}
	for _, c := range cases {
		s, err := Effective(c.ceiling, c.known, c.requested)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("Effective(%v, %v, %q) err = %v, want %q", c.ceiling, c.known, c.requested, err, c.err)
			}
			continue
		}
		if err != nil || s.Role != c.want {
			t.Errorf("Effective(%v, %v, %q) = %v, %v; want %v", c.ceiling, c.known, c.requested, s.Role, err, c.want)
		}
	}
}

func TestRoleFor(t *testing.T) {
	for level, want := range map[forge.AccessLevel]Role{
		forge.AccessAdmin: Producer, forge.AccessMaintain: Director, forge.AccessWrite: Talent,
		forge.AccessRead: Audience, forge.AccessNone: Audience,
	} {
		if got := RoleFor(level); got != want {
			t.Errorf("RoleFor(%d) = %v, want %v", level, got, want)
		}
	}
}

func TestPushRole(t *testing.T) {
	cfg := trunk()
	cfg.Git.Branches["stable"] = "^stable$"
	cfg.Workflow.Branches = append(cfg.Workflow.Branches, config.BranchClass{ID: "stable", Match: "stable", Landing: config.LandingDirect})
	talent, director, audience := Standing{Role: Talent}, Standing{Role: Director}, Standing{Role: Audience}

	if f := PushRole(cfg, "feature/x", talent); f != nil {
		t.Errorf("talent pushes working branches: %+v", f)
	}
	if f := PushRole(cfg, "feature/x", audience); f == nil || !strings.Contains(f.Message, "talent") {
		t.Errorf("audience doesn't push: %+v", f)
	}
	if f := PushRole(cfg, "stable", talent); f == nil || !strings.Contains(f.Message, "director") {
		t.Errorf("a long-lived direct branch needs a director: %+v", f)
	}
	if f := PushRole(cfg, "stable", director); f != nil {
		t.Errorf("director pushes it: %+v", f)
	}
	// main takes merge requests: the landing rule refuses the push, not the role.
	if f := PushRole(cfg, "main", talent); f != nil {
		t.Errorf("merge-request class is the landing rule's: %+v", f)
	}
	known := Standing{Role: Talent, Ceiling: Talent, Known: true}
	if f := PushRole(cfg, "stable", known); f == nil || !strings.Contains(f.Teach, "ask a director") {
		t.Errorf("a ceiling below the need says who can: %+v", f)
	}
}

func TestReleaseRole(t *testing.T) {
	cfg := trunk()
	if f := ReleaseRole(cfg, Standing{Role: Director}); f == nil || !strings.Contains(f.Message, "producer") {
		t.Errorf("cutting a release is a producer's call: %+v", f)
	}
	if f := ReleaseRole(cfg, Standing{Role: Producer}); f != nil {
		t.Errorf("producer cuts it: %+v", f)
	}
	if f := ReleaseRole(&config.Config{}, Standing{Role: Talent}); f != nil {
		t.Errorf("no workflow, no rule: %+v", f)
	}
}
