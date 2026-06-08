package triage

import (
	"encoding/json"
	"fmt"
	"os"
)

// Metadata is the structured sidecar JSON produced by the AI alongside
// the markdown assessment.
type Metadata struct {
	Recommendation    string `json:"recommendation"`
	Confidence        string `json:"confidence"`
	AutoFixLikelihood *int   `json:"autoFixLikelihood"`
}

// ReadMetadata reads and parses the metadata sidecar file.
// Returns nil (no error) if the file does not exist.
func ReadMetadata(path string) (*Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read metadata file: %w", err)
	}

	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse metadata JSON: %w", err)
	}
	return &m, nil
}

// ShouldApplyAutoFixLabel returns true if the metadata indicates an
// AUTO_FIX recommendation with likelihood at or above the threshold.
func (m *Metadata) ShouldApplyAutoFixLabel(threshold int) bool {
	if m == nil {
		return false
	}
	if m.Recommendation != "AUTO_FIX" {
		return false
	}
	if m.AutoFixLikelihood == nil {
		return false
	}
	return *m.AutoFixLikelihood >= threshold
}

// TriageOutcome determines the triage label category based on the
// recommendation. Returns "autofix", "missing_info", or "not_fixable".
// An AUTO_FIX recommendation that falls below threshold is treated as
// not_fixable since the triage bot assessed it but deemed it unsuitable.
func (m *Metadata) TriageOutcome(autofixThreshold int) string {
	if m == nil {
		return "not_fixable"
	}
	switch m.Recommendation {
	case "NEEDS_INFO":
		return "missing_info"
	case "AUTO_FIX":
		if m.AutoFixLikelihood != nil && *m.AutoFixLikelihood >= autofixThreshold {
			return "autofix"
		}
		return "not_fixable"
	default:
		return "not_fixable"
	}
}
