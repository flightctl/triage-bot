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
