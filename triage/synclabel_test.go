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

func TestSyncLabel_AutofixApplied(t *testing.T) {
	mock := &mockJiraClient{}
	p := newTestProcessor(mock)
	meta := &Metadata{Recommendation: "AUTO_FIX", AutoFixLikelihood: likelihood(85)}

	p.syncLabel(context.Background(), "TEST-1", nil, meta)

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(mock.calls), mock.calls)
	}
	if mock.calls[0] != (labelCall{"add", "TEST-1", "ai-autofix-candidate"}) {
		t.Errorf("unexpected call: %+v", mock.calls[0])
	}
}

func TestSyncLabel_MissingInfoApplied(t *testing.T) {
	mock := &mockJiraClient{}
	p := newTestProcessor(mock)
	meta := &Metadata{Recommendation: "NEEDS_INFO"}

	p.syncLabel(context.Background(), "TEST-1", nil, meta)

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(mock.calls), mock.calls)
	}
	if mock.calls[0] != (labelCall{"add", "TEST-1", "triage-missing-info"}) {
		t.Errorf("unexpected call: %+v", mock.calls[0])
	}
}

func TestSyncLabel_NotFixableApplied(t *testing.T) {
	mock := &mockJiraClient{}
	p := newTestProcessor(mock)
	meta := &Metadata{Recommendation: "WONT_FIX"}

	p.syncLabel(context.Background(), "TEST-1", nil, meta)

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(mock.calls), mock.calls)
	}
	if mock.calls[0] != (labelCall{"add", "TEST-1", "triage-not-fixable"}) {
		t.Errorf("unexpected call: %+v", mock.calls[0])
	}
}

func TestSyncLabel_StaleLabelRemoved(t *testing.T) {
	mock := &mockJiraClient{}
	p := newTestProcessor(mock)
	meta := &Metadata{Recommendation: "NEEDS_INFO"}
	existing := []string{"ai-autofix-candidate"}

	p.syncLabel(context.Background(), "TEST-1", existing, meta)

	if len(mock.calls) != 2 {
		t.Fatalf("expected 2 calls (add + remove), got %d: %+v", len(mock.calls), mock.calls)
	}
	if mock.calls[0] != (labelCall{"add", "TEST-1", "triage-missing-info"}) {
		t.Errorf("expected add missing-info, got: %+v", mock.calls[0])
	}
	if mock.calls[1] != (labelCall{"remove", "TEST-1", "ai-autofix-candidate"}) {
		t.Errorf("expected remove autofix, got: %+v", mock.calls[1])
	}
}

func TestSyncLabel_NilMetadata_NoOp(t *testing.T) {
	mock := &mockJiraClient{}
	p := newTestProcessor(mock)

	p.syncLabel(context.Background(), "TEST-1", []string{"ai-autofix-candidate"}, nil)

	if len(mock.calls) != 0 {
		t.Fatalf("expected no calls for nil metadata, got %d: %+v", len(mock.calls), mock.calls)
	}
}

func TestSyncLabel_Idempotent(t *testing.T) {
	mock := &mockJiraClient{}
	p := newTestProcessor(mock)
	meta := &Metadata{Recommendation: "AUTO_FIX", AutoFixLikelihood: likelihood(85)}
	existing := []string{"ai-autofix-candidate"}

	p.syncLabel(context.Background(), "TEST-1", existing, meta)

	if len(mock.calls) != 0 {
		t.Fatalf("expected no calls when correct label exists, got %d: %+v", len(mock.calls), mock.calls)
	}
}

func TestSyncLabel_DisabledLabel_StillCleansUp(t *testing.T) {
	mock := &mockJiraClient{}
	p := newTestProcessor(mock)
	p.cfg.Triage.MissingInfoLabel = ""
	meta := &Metadata{Recommendation: "NEEDS_INFO"}
	existing := []string{"ai-autofix-candidate"}

	p.syncLabel(context.Background(), "TEST-1", existing, meta)

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call (remove stale), got %d: %+v", len(mock.calls), mock.calls)
	}
	if mock.calls[0] != (labelCall{"remove", "TEST-1", "ai-autofix-candidate"}) {
		t.Errorf("expected remove stale autofix label, got: %+v", mock.calls[0])
	}
}

func TestSyncLabel_DryRun(t *testing.T) {
	mock := &mockJiraClient{}
	p := newTestProcessor(mock)
	p.cfg.DryRun = true
	meta := &Metadata{Recommendation: "AUTO_FIX", AutoFixLikelihood: likelihood(85)}

	p.syncLabel(context.Background(), "TEST-1", nil, meta)

	if len(mock.calls) != 0 {
		t.Fatalf("expected no Jira calls in dry-run, got %d: %+v", len(mock.calls), mock.calls)
	}
}

func TestSyncLabel_BelowThreshold_NotFixable(t *testing.T) {
	mock := &mockJiraClient{}
	p := newTestProcessor(mock)
	meta := &Metadata{Recommendation: "AUTO_FIX", AutoFixLikelihood: likelihood(50)}

	p.syncLabel(context.Background(), "TEST-1", nil, meta)

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(mock.calls), mock.calls)
	}
	if mock.calls[0] != (labelCall{"add", "TEST-1", "triage-not-fixable"}) {
		t.Errorf("expected not-fixable for below-threshold, got: %+v", mock.calls[0])
	}
}
