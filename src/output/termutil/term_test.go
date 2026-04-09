package termutil

import (
	"bytes"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/output/layout"
)

func TestContentWidth_COLUMNS(t *testing.T) {
	tests := []struct {
		name    string
		columns string
		want    int
	}{
		{
			name:    "narrow 80-col terminal",
			columns: "80",
			want:    80 - layout.FramePrefix, // 74 — must NOT be clamped up to 120
		},
		{
			name:    "standard 120-col terminal",
			columns: "120",
			want:    120 - layout.FramePrefix, // 114
		},
		{
			name:    "wide 200-col terminal",
			columns: "200",
			want:    200 - layout.FramePrefix, // 194
		},
		{
			name:    "minimum gate: 40-col terminal",
			columns: "40",
			want:    40 - layout.FramePrefix, // 34 — above minContentWidth floor
		},
		{
			name:    "below gate: 39-col terminal ignored, fallback used",
			columns: "39",
			want:    layout.DefaultContentWidth, // gate rejects < 40, falls to default
		},
		{
			name:    "below gate: COLUMNS=10 ignored, fallback used",
			columns: "10",
			want:    layout.DefaultContentWidth, // gate rejects < 40, falls to default
		},
		{
			name:    "invalid COLUMNS, fallback used",
			columns: "not-a-number",
			want:    layout.DefaultContentWidth,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("COLUMNS", tt.columns)
			got := ContentWidth(&bytes.Buffer{}) // non-*os.File writer → skips TTY path
			if got != tt.want {
				t.Errorf("ContentWidth(COLUMNS=%q) = %d; want %d", tt.columns, got, tt.want)
			}
		})
	}
}

func TestContentWidth_NoTTY_NoColumns_ReturnsFallback(t *testing.T) {
	t.Setenv("COLUMNS", "")
	got := ContentWidth(&bytes.Buffer{})
	if got != layout.DefaultContentWidth {
		t.Errorf("ContentWidth() with no TTY and no COLUMNS = %d; want %d (DefaultContentWidth)",
			got, layout.DefaultContentWidth)
	}
}

func TestClamp(t *testing.T) {
	tests := []struct {
		budget int
		want   int
	}{
		{budget: 74, want: 74},   // 80-col terminal budget — must pass through
		{budget: 114, want: 114}, // 120-col terminal budget
		{budget: 34, want: 34},   // 40-col terminal minimum
		{budget: 20, want: 20},   // at minContentWidth floor
		{budget: 10, want: 20},   // below floor — clamped up
		{budget: 0, want: 20},    // zero — clamped up
		{budget: -5, want: 20},   // negative — clamped up
	}
	for _, tt := range tests {
		got := clamp(tt.budget)
		if got != tt.want {
			t.Errorf("clamp(%d) = %d; want %d", tt.budget, got, tt.want)
		}
	}
}
