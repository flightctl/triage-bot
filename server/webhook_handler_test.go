package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"triage-bot/jira"
	"triage-bot/scanner"
)

type mockProcessor struct {
	called chan jira.JiraIssue
}

func (m *mockProcessor) Process(_ context.Context, issue jira.JiraIssue) error {
	if m.called != nil {
		m.called <- issue
	}
	return nil
}

func testWebhookHandler(t *testing.T, processor IssueProcessor) *WebhookHandler {
	t.Helper()
	h, err := NewWebhookHandler(processor, scanner.NewInFlight(), context.Background(), "test-secret", 3, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestWebhookHandler_NonPostRejected(t *testing.T) {
	h := testWebhookHandler(t, &mockProcessor{})
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestWebhookHandler_InvalidSignatureRejected(t *testing.T) {
	h := testWebhookHandler(t, &mockProcessor{})
	body := `{"webhookEvent":"jira:issue_updated","issue":{"key":"PROJ-1"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature", "sha256=invalid")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestWebhookHandler_MissingSignatureRejected(t *testing.T) {
	h := testWebhookHandler(t, &mockProcessor{})
	body := `{"webhookEvent":"jira:issue_updated","issue":{"key":"PROJ-1"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestWebhookHandler_InvalidJSONRejected(t *testing.T) {
	h := testWebhookHandler(t, &mockProcessor{})
	body := `not json`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature", ComputeSignature("test-secret", []byte(body)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestWebhookHandler_MissingIssueKeyAccepted(t *testing.T) {
	h := testWebhookHandler(t, &mockProcessor{})
	body := `{"webhookEvent":"jira:issue_updated","issue":{}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature", ComputeSignature("test-secret", []byte(body)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestWebhookHandler_ValidEventDispatched(t *testing.T) {
	proc := &mockProcessor{called: make(chan jira.JiraIssue, 1)}
	h := testWebhookHandler(t, proc)
	body := `{"webhookEvent":"jira:issue_updated","issue":{"key":"PROJ-123","fields":{"project":{"key":"PROJ"}}}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature", ComputeSignature("test-secret", []byte(body)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	select {
	case issue := <-proc.called:
		if issue.Key != "PROJ-123" {
			t.Errorf("issue key = %q, want PROJ-123", issue.Key)
		}
	case <-time.After(2 * time.Second):
		t.Error("processor.Process was not called within timeout")
	}
}

func TestWebhookHandler_EmptySecretRejected(t *testing.T) {
	_, err := NewWebhookHandler(&mockProcessor{}, scanner.NewInFlight(), context.Background(), "", 3, zap.NewNop())
	if err == nil {
		t.Error("expected error for empty secret")
	}
}

func TestWebhookHandler_InFlightDedup(t *testing.T) {
	proc := &mockProcessor{called: make(chan jira.JiraIssue, 2)}
	inFlight := scanner.NewInFlight()
	h, _ := NewWebhookHandler(proc, inFlight, context.Background(), "test-secret", 3, zap.NewNop())

	inFlight.TryAcquire("PROJ-456")

	body := `{"webhookEvent":"jira:issue_updated","issue":{"key":"PROJ-456","fields":{"project":{"key":"PROJ"}}}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature", ComputeSignature("test-secret", []byte(body)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	select {
	case <-proc.called:
		t.Error("processor should NOT have been called for in-flight issue")
	case <-time.After(100 * time.Millisecond):
		// expected
	}
}
