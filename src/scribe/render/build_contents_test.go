package render

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/manifest"
)

func TestBuildContentsModule_DetailsWrap(t *testing.T) {
	m := &manifest.Manifest{
		Inventories: manifest.Invs{
			Versions: []manifest.InvItem{
				{Name: "python", Version: "3.14.3", SourceRef: "FROM python:3.14.3-alpine3.23"},
				{Name: "alpine", Version: "3.23", SourceRef: "FROM python:3.14.3-alpine3.23"},
			},
		},
	}

	mod := BuildContentsModule{
		Manifest: m,
		Section:  "inventories.versions",
		Renderer: "versions",
		Wrap:     "details",
		Summary:  "Base image contents",
	}

	got := mod.Render()

	if !strings.HasPrefix(got, "<details>\n<summary>Base image contents</summary>") {
		t.Errorf("expected <details> wrapper with summary, got:\n%s", got)
	}
	if !strings.HasSuffix(got, "</details>") {
		t.Errorf("expected closing </details>, got:\n%s", got)
	}
	if !strings.Contains(got, "Built from `python:3.14.3-alpine3.23`.") {
		t.Errorf("expected Built from header inside wrapper, got:\n%s", got)
	}
	if !strings.Contains(got, "| python | 3.14.3 |") {
		t.Errorf("expected version table inside wrapper, got:\n%s", got)
	}
}

func TestBuildContentsModule_NoWrap(t *testing.T) {
	m := &manifest.Manifest{
		Inventories: manifest.Invs{
			Versions: []manifest.InvItem{
				{Name: "alpine", Version: "3.23", SourceRef: "FROM alpine:3.23"},
			},
		},
	}

	mod := BuildContentsModule{
		Manifest: m,
		Section:  "inventories.versions",
		Renderer: "versions",
	}

	got := mod.Render()

	if strings.Contains(got, "<details>") {
		t.Errorf("should not have <details> when wrap is empty, got:\n%s", got)
	}
	if !strings.Contains(got, "Built from `alpine:3.23`.") {
		t.Errorf("expected Built from header, got:\n%s", got)
	}
}

func TestBuildContentsModule_UnknownWrapRefusesToRender(t *testing.T) {
	m := &manifest.Manifest{
		Inventories: manifest.Invs{
			Versions: []manifest.InvItem{
				{Name: "alpine", Version: "3.23"},
			},
		},
	}

	mod := BuildContentsModule{
		Manifest: m,
		Section:  "inventories.versions",
		Renderer: "versions",
		Wrap:     "bogus",
	}

	got := mod.Render()

	if got != "" {
		t.Errorf("unknown wrap should return empty string, got:\n%s", got)
	}
}
