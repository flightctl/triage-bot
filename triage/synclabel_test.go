package triage

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"triage-bot/config"
	"triage-bot/jira"
)

type labelCall struct {
	method string // "add" or "remove"
	key    string
	label  string
}

type mockJiraClient struct {
	calls []labelCall
}

func (m *mockJiraClient) GetComments(_ context.Context, _ string) ([]jira.JiraComment, error) {
	return nil, nil
}
func (m *mockJiraClient) AddComment(_ context.Context, _, _ string) error { return nil }
func (m *mockJiraClient) UpdateComment(_ context.Context, _, _, _ string) error {
	return nil
}
func (m *mockJiraClient) AddCommentADF(_ context.Context, _ string, _ map[string]any) error {
	return nil
}
func (m *mockJiraClient) UpdateCommentADF(_ context.Context, _, _ string, _ map[string]any) error {
	return nil
}
func (m *mockJiraClient) AddLabel(_ context.Context, key, label string) error {
	m.calls = append(m.calls, labelCall{"add", key, label})
	return nil
}
func (m *mockJiraClient) RemoveLabel(_ context.Context, key, label string) error {
	m.calls = append(m.calls, labelCall{"remove", key, label})
	return nil
}

func newTestProcessor(mock *mockJiraClient) *Processor {
	return &Processor{
		jira: mock,
		cfg: config.Config{
			Triage: config.TriageConfig{
				AutoFixLabel:     "ai-autofix-candidate",
				AutoFixThreshold: 60,
				MissingInfoLabel: "triage-missing-info",
				NotFixableLabel:  "triage-not-fixable",
			},
		},
		logger: zap.NewNop(),
	}
}

func likelihood(v int) *int { return &v }
func strPtr(s string) *string { return &s }

func TestSyncLabel(t *testing.T) {
	tests := []struct {
		name             string
		meta             *Metadata
		existing         []string
		dryRun           bool
		missingInfoLabel *string // override; nil = use default
		expectedCalls    []labelCall
	}{
		{
			name:          "autofix applied",
			meta:          &Metadata{Recommendation: "AUTO_FIX", AutoFixLikelihood: likelihood(85)},
			expectedCalls: []labelCall{{"add", "TEST-1", "ai-autofix-candidate"}},
		},
		{
			name:          "missing info applied",
			meta:          &Metadata{Recommendation: "NEEDS_INFO"},
			expectedCalls: []labelCall{{"add", "TEST-1", "triage-missing-info"}},
		},
		{
			name:          "not fixable applied",
			meta:          &Metadata{Recommendation: "WONT_FIX"},
			expectedCalls: []labelCall{{"add", "TEST-1", "triage-not-fixable"}},
		},
		{
			name:     "stale label removed on re-triage",
			meta:     &Metadata{Recommendation: "NEEDS_INFO"},
			existing: []string{"ai-autofix-candidate"},
			expectedCalls: []labelCall{
				{"add", "TEST-1", "triage-missing-info"},
				{"remove", "TEST-1", "ai-autofix-candidate"},
			},
		},
		{
			name:          "nil metadata is no-op",
			meta:          nil,
			existing:      []string{"ai-autofix-candidate"},
			expectedCalls: nil,
		},
		{
			name:          "idempotent when correct label present",
			meta:          &Metadata{Recommendation: "AUTO_FIX", AutoFixLikelihood: likelihood(85)},
			existing:      []string{"ai-autofix-candidate"},
			expectedCalls: nil,
		},
		{
			name:             "disabled label still cleans up stale",
			meta:             &Metadata{Recommendation: "NEEDS_INFO"},
			existing:         []string{"ai-autofix-candidate"},
			missingInfoLabel: strPtr(""),
			expectedCalls:    []labelCall{{"remove", "TEST-1", "ai-autofix-candidate"}},
		},
		{
			name:          "dry run makes no Jira calls",
			meta:          &Metadata{Recommendation: "AUTO_FIX", AutoFixLikelihood: likelihood(85)},
			dryRun:        true,
			expectedCalls: nil,
		},
		{
			name:          "below threshold yields not-fixable",
			meta:          &Metadata{Recommendation: "AUTO_FIX", AutoFixLikelihood: likelihood(50)},
			expectedCalls: []labelCall{{"add", "TEST-1", "triage-not-fixable"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockJiraClient{}
			p := newTestProcessor(mock)
			if tt.dryRun {
				p.cfg.DryRun = true
			}
			if tt.missingInfoLabel != nil {
				p.cfg.Triage.MissingInfoLabel = *tt.missingInfoLabel
			}

			p.syncLabel(context.Background(), "TEST-1", tt.existing, tt.meta)

			if len(mock.calls) != len(tt.expectedCalls) {
				t.Fatalf("expected %d calls, got %d: %+v", len(tt.expectedCalls), len(mock.calls), mock.calls)
			}
			for i, want := range tt.expectedCalls {
				if mock.calls[i] != want {
					t.Errorf("call[%d] = %+v, want %+v", i, mock.calls[i], want)
				}
			}
		})
	}
}
