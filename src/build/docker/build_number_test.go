package docker

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

func planWithVersionTag(tag string) *build.BuildPlan {
	return &build.BuildPlan{Steps: []build.BuildStep{{
		Registries: []build.RegistryTarget{{
			URL:  "https://ghcr.io",
			Path: "homelabhd/synapse",
			Tags: []string{"latest", tag},
		}},
	}}}
}

func TestResolveBuildNumber(t *testing.T) {
	const (
		plan = "planABC"
		base = "sha256:base"
	)
	cases := []struct {
		name    string
		labels  map[string]string
		readErr error
		want    string
	}{
		{"first publish (tag absent)", map[string]string{}, nil, "1"},
		{"published but unnumbered", map[string]string{"org.stagefreight.plan.sha256": plan}, nil, "1"},
		{"same content reuses", map[string]string{
			"org.stagefreight.build.number":        "3",
			"org.stagefreight.plan.sha256":         plan,
			"org.opencontainers.image.base.digest": base,
		}, nil, "3"},
		{"changed plan increments", map[string]string{
			"org.stagefreight.build.number":        "3",
			"org.stagefreight.plan.sha256":         "different",
			"org.opencontainers.image.base.digest": base,
		}, nil, "4"},
		{"changed base increments", map[string]string{
			"org.stagefreight.build.number":        "3",
			"org.stagefreight.plan.sha256":         plan,
			"org.opencontainers.image.base.digest": "sha256:newbase",
		}, nil, "4"},
		{"read failure omits", nil, errors.New("timeout"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := readPublishedLabels
			readPublishedLabels = func(_ context.Context, _, _ string) (map[string]string, error) {
				return tc.labels, tc.readErr
			}
			defer func() { readPublishedLabels = orig }()

			c := &crucibleContributor{}
			var w bytes.Buffer
			got := c.resolveBuildNumber(context.Background(), &w, planWithVersionTag("1.164.1"), plan, base, "1.164.1")
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if tc.readErr != nil && w.Len() == 0 {
				t.Error("a read failure must be logged loudly, not silently omitted")
			}
		})
	}
}

// No registry target carrying the version tag → no lineage to count from → omit.
func TestResolveBuildNumberNoVersionTag(t *testing.T) {
	c := &crucibleContributor{}
	var w bytes.Buffer
	plan := &build.BuildPlan{Steps: []build.BuildStep{{
		Registries: []build.RegistryTarget{{URL: "https://ghcr.io", Path: "x/y", Tags: []string{"latest"}}},
	}}}
	if got := c.resolveBuildNumber(context.Background(), &w, plan, "p", "b", "1.164.1"); got != "" {
		t.Errorf("got %q, want empty (no version tag)", got)
	}
}

func TestPublishedVersionRef(t *testing.T) {
	// v-prefixed version matches a bare published tag and vice versa; scheme is stripped.
	img, tag := publishedVersionRef(planWithVersionTag("1.164.1"), "v1.164.1")
	if img != "ghcr.io/homelabhd/synapse" || tag != "1.164.1" {
		t.Errorf("got %q:%q", img, tag)
	}
}
