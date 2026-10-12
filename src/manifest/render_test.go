package manifest

import (
	"strings"
	"testing"
)

func TestRenderVersions_SingleStage(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{
			"name":       "python",
			"version":    "3.14.3",
			"source_ref": "FROM python:3.14.3-alpine3.23",
		},
		map[string]interface{}{
			"name":       "alpine",
			"version":    "3.23",
			"source_ref": "FROM python:3.14.3-alpine3.23",
		},
	}

	got, err := RenderVersions(data)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, "Built from `python:3.14.3-alpine3.23`.") {
		t.Errorf("expected Built from header, got:\n%s", got)
	}
	if !strings.Contains(got, "| Name | Version |") {
		t.Errorf("expected 2-column table header, got:\n%s", got)
	}
	if strings.Contains(got, "Stage") {
		t.Errorf("should not have Stage column for single-stage, got:\n%s", got)
	}
	if strings.Contains(got, "source_ref") || strings.Contains(got, "Source Ref") {
		t.Errorf("source_ref must not appear as table column, got:\n%s", got)
	}
	if !strings.Contains(got, "| python | 3.14.3 |") {
		t.Errorf("expected python row, got:\n%s", got)
	}
	if !strings.Contains(got, "| alpine | 3.23 |") {
		t.Errorf("expected alpine row, got:\n%s", got)
	}
}

func TestRenderVersions_MultiStage(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{
			"name":       "golang",
			"version":    "1.26.1",
			"source_ref": "FROM golang:1.26.1-alpine3.23",
			"stage":      "builder",
		},
		map[string]interface{}{
			"name":       "alpine",
			"version":    "3.23",
			"source_ref": "FROM golang:1.26.1-alpine3.23",
			"stage":      "builder",
		},
		map[string]interface{}{
			"name":       "alpine",
			"version":    "3.23",
			"source_ref": "FROM alpine:3.23",
			"stage":      "runtime",
		},
	}

	got, err := RenderVersions(data)
	if err != nil {
		t.Fatal(err)
	}

	// Two unique refs, comma-separated.
	if !strings.Contains(got, "Built from `golang:1.26.1-alpine3.23`, `alpine:3.23`.") {
		t.Errorf("expected multi-ref Built from header, got:\n%s", got)
	}
	// 3-column table with Stage.
	if !strings.Contains(got, "| Name | Version | Stage |") {
		t.Errorf("expected 3-column table header with Stage, got:\n%s", got)
	}
	if !strings.Contains(got, "| golang | 1.26.1 | builder |") {
		t.Errorf("expected golang builder row, got:\n%s", got)
	}
	if !strings.Contains(got, "| alpine | 3.23 | runtime |") {
		t.Errorf("expected alpine runtime row, got:\n%s", got)
	}
}

func TestRenderVersions_Empty(t *testing.T) {
	data := []interface{}{}

	got, err := RenderVersions(data)
	if err != nil {
		t.Fatal(err)
	}
	if got != "No items" {
		t.Errorf("expected 'No items', got %q", got)
	}
}

func TestRenderVersions_NoSourceRef(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{
			"name":    "python",
			"version": "3.14.3",
		},
		map[string]interface{}{
			"name":    "alpine",
			"version": "3.23",
		},
	}

	got, err := RenderVersions(data)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(got, "Built from") {
		t.Errorf("should not have Built from header when no source_ref, got:\n%s", got)
	}
	if !strings.Contains(got, "| Name | Version |") {
		t.Errorf("expected table even without header, got:\n%s", got)
	}
	if !strings.Contains(got, "| python | 3.14.3 |") {
		t.Errorf("expected python row, got:\n%s", got)
	}
}

func TestRenderVersions_RefDeduplication(t *testing.T) {
	// Same source_ref on multiple items should appear only once in header.
	data := []interface{}{
		map[string]interface{}{
			"name":       "python",
			"version":    "3.14.3",
			"source_ref": "FROM python:3.14.3-alpine3.23",
		},
		map[string]interface{}{
			"name":       "alpine",
			"version":    "3.23",
			"source_ref": "FROM python:3.14.3-alpine3.23",
		},
	}

	got, err := RenderVersions(data)
	if err != nil {
		t.Fatal(err)
	}

	// Should appear exactly once.
	count := strings.Count(got, "python:3.14.3-alpine3.23")
	if count != 1 {
		t.Errorf("expected source ref to appear once in header, appeared %d times:\n%s", count, got)
	}
}

func TestRenderVersions_FROMPrefixStripped(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{
			"name":       "alpine",
			"version":    "3.23",
			"source_ref": "FROM alpine:3.23",
		},
	}

	got, err := RenderVersions(data)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(got, "FROM ") {
		t.Errorf("FROM prefix should be stripped from header, got:\n%s", got)
	}
	if !strings.Contains(got, "Built from `alpine:3.23`.") {
		t.Errorf("expected clean ref in header, got:\n%s", got)
	}
}

// include: keeps only the named items, in the order the patterns are declared — a
// curated subset of a section that would otherwise print everything.
func TestFilterItems(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{"name": "requests", "version": "2.32"},
		map[string]interface{}{"name": "hrms", "version": "16.50.0"},
		map[string]interface{}{"name": "Frappe", "version": "16.51.0"},
		map[string]interface{}{"name": "erpnext", "version": "16.50.0"},
		map[string]interface{}{"name": "frappe-extras", "version": "1.0"},
	}
	out, err := FilterItems(data, []string{"frappe", "erpnext", "hrms"})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := toMapSlice(out)
	var names []string
	for _, it := range items {
		names = append(names, formatCell(it["name"]))
	}
	want := []string{"Frappe", "erpnext", "hrms"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v (declared order, case-insensitive, exact glob)", names, want)
		}
	}
	// globs work, and an empty pattern list is a no-op
	out, _ = FilterItems(data, []string{"frappe*"})
	if items, _ := toMapSlice(out); len(items) != 2 {
		t.Fatalf("glob: got %d items, want 2", len(items))
	}
	if same, _ := FilterItems(data, nil); len(same.([]interface{})) != len(data) {
		t.Fatal("no patterns must return the section unchanged")
	}
	// a non-array section passes through untouched
	if kv, _ := FilterItems(map[string]interface{}{"os": "linux"}, []string{"x"}); kv == nil {
		t.Fatal("non-array section must pass through")
	}
}
