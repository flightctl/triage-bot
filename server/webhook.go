package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"triage-bot/jira"
)

// IssueProcessor processes a single Jira issue.
type IssueProcessor interface {
	Process(issue jira.JiraIssue) error
}

// WebhookHandler handles incoming Jira webhook events.
type WebhookHandler struct {
	processor IssueProcessor
	secret    string
	sem       chan struct{}
	logger    *zap.Logger
}

func NewWebhookHandler(processor IssueProcessor, secret string, maxConcurrent int, logger *zap.Logger) *WebhookHandler {
	return &WebhookHandler{
		processor: processor,
		secret:    secret,
		sem:       make(chan struct{}, maxConcurrent),
		logger:    logger,
	}
}

// jiraWebhookPayload is the relevant subset of a Jira webhook POST body.
type jiraWebhookPayload struct {
	WebhookEvent string         `json:"webhookEvent"`
	Issue        jira.JiraIssue `json:"issue"`
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Error("Failed to read webhook body", zap.Error(err))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if h.secret != "" {
		if !h.verifySignature(r.Header.Get("X-Hub-Signature"), body) {
			h.logger.Warn("Webhook signature verification failed")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	var payload jiraWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.logger.Error("Failed to parse webhook payload", zap.Error(err))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if payload.Issue.Key == "" {
		h.logger.Warn("Webhook payload missing issue key, ignoring")
		w.WriteHeader(http.StatusOK)
		return
	}

	h.logger.Info("Received webhook event",
		zap.String("event", payload.WebhookEvent),
		zap.String("issue", payload.Issue.Key))

	// Process asynchronously so we respond to Jira within its timeout.
	// Use a semaphore to bound concurrent processing.
	select {
	case h.sem <- struct{}{}:
		go func() {
			defer func() { <-h.sem }()
			if err := h.processor.Process(payload.Issue); err != nil {
				h.logger.Error("Webhook-triggered processing failed",
					zap.String("issue", payload.Issue.Key),
					zap.Error(err))
			}
		}()
	default:
		h.logger.Warn("Webhook throttled, too many concurrent requests",
			zap.String("issue", payload.Issue.Key))
	}

	w.WriteHeader(http.StatusOK)
}

// verifySignature checks the HMAC-SHA256 signature from Jira.
// The header format is "sha256=<hex-encoded-signature>".
func (h *WebhookHandler) verifySignature(header string, body []byte) bool {
	if header == "" {
		return false
	}

	parts := strings.SplitN(header, "=", 2)
	if len(parts) != 2 || parts[0] != "sha256" {
		return false
	}

	sig, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(h.secret))
	mac.Write(body)
	expected := mac.Sum(nil)

	return hmac.Equal(sig, expected)
}

// VerifySignature is exported for testing.
func VerifySignature(secret, header string, body []byte) bool {
	h := &WebhookHandler{secret: secret}
	return h.verifySignature(header, body)
}

// ComputeSignature generates the expected X-Hub-Signature header value for testing.
func ComputeSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return fmt.Sprintf("sha256=%s", hex.EncodeToString(mac.Sum(nil)))
}
