package triage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMetadata_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.meta.json")

	content := `{"recommendation": "AUTO_FIX", "confidence": "High", "autoFixLikelihood": 85}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Recommendation != "AUTO_FIX" {
		t.Errorf("recommendation = %q, want AUTO_FIX", m.Recommendation)
	}
	if m.AutoFixLikelihood == nil || *m.AutoFixLikelihood != 85 {
		t.Errorf("autoFixLikelihood = %v, want 85", m.AutoFixLikelihood)
	}
}

func TestReadMetadata_Missing(t *testing.T) {
	m, err := ReadMetadata("/nonexistent/path.json")
	if err != nil {
		t.Fatalf("unexpected error for missing file: %v", err)
	}
	if m != nil {
		t.Error("expected nil for missing file")
	}
}

func TestReadMetadata_NullLikelihood(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.meta.json")

	content := `{"recommendation": "FIX_NOW", "confidence": "High", "autoFixLikelihood": null}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.AutoFixLikelihood != nil {
		t.Errorf("expected nil autoFixLikelihood, got %d", *m.AutoFixLikelihood)
	}
}

func TestShouldApplyAutoFixLabel(t *testing.T) {
	likelihood := func(v int) *int { return &v }

	tests := []struct {
		name      string
		meta      *Metadata
		threshold int
		want      bool
	}{
		{name: "nil metadata", meta: nil, threshold: 60, want: false},
		{name: "not auto_fix", meta: &Metadata{Recommendation: "FIX_NOW"}, threshold: 60, want: false},
		{name: "auto_fix nil likelihood", meta: &Metadata{Recommendation: "AUTO_FIX"}, threshold: 60, want: false},
		{name: "below threshold", meta: &Metadata{Recommendation: "AUTO_FIX", AutoFixLikelihood: likelihood(50)}, threshold: 60, want: false},
		{name: "at threshold", meta: &Metadata{Recommendation: "AUTO_FIX", AutoFixLikelihood: likelihood(60)}, threshold: 60, want: true},
		{name: "above threshold", meta: &Metadata{Recommendation: "AUTO_FIX", AutoFixLikelihood: likelihood(85)}, threshold: 60, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.meta.ShouldApplyAutoFixLabel(tt.threshold)
			if got != tt.want {
				t.Errorf("ShouldApplyAutoFixLabel = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTriageOutcome(t *testing.T) {
	likelihood := func(v int) *int { return &v }

	tests := []struct {
		name      string
		meta      *Metadata
		threshold int
		want      string
	}{
		{name: "nil metadata", meta: nil, threshold: 60, want: "not_fixable"},
		{name: "NEEDS_INFO", meta: &Metadata{Recommendation: "NEEDS_INFO"}, threshold: 60, want: "missing_info"},
		{name: "AUTO_FIX above threshold", meta: &Metadata{Recommendation: "AUTO_FIX", AutoFixLikelihood: likelihood(85)}, threshold: 60, want: "autofix"},
		{name: "AUTO_FIX at threshold", meta: &Metadata{Recommendation: "AUTO_FIX", AutoFixLikelihood: likelihood(60)}, threshold: 60, want: "autofix"},
		{name: "AUTO_FIX below threshold", meta: &Metadata{Recommendation: "AUTO_FIX", AutoFixLikelihood: likelihood(50)}, threshold: 60, want: "not_fixable"},
		{name: "AUTO_FIX nil likelihood", meta: &Metadata{Recommendation: "AUTO_FIX"}, threshold: 60, want: "not_fixable"},
		{name: "WONT_FIX", meta: &Metadata{Recommendation: "WONT_FIX"}, threshold: 60, want: "not_fixable"},
		{name: "CLOSE", meta: &Metadata{Recommendation: "CLOSE"}, threshold: 60, want: "not_fixable"},
		{name: "DUPLICATE", meta: &Metadata{Recommendation: "DUPLICATE"}, threshold: 60, want: "not_fixable"},
		{name: "FIX_NOW", meta: &Metadata{Recommendation: "FIX_NOW"}, threshold: 60, want: "not_fixable"},
		{name: "BACKLOG", meta: &Metadata{Recommendation: "BACKLOG"}, threshold: 60, want: "not_fixable"},
		{name: "ESCALATE", meta: &Metadata{Recommendation: "ESCALATE"}, threshold: 60, want: "not_fixable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.meta.TriageOutcome(tt.threshold)
			if got != tt.want {
				t.Errorf("TriageOutcome = %q, want %q", got, tt.want)
			}
		})
	}
}
