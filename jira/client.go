package jira

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	maxAttempts           = 2
	maxRetryWaitSeconds   = 60
	initialBackoffSeconds = 1
	maxBackoffSeconds     = 16
	maxJitterSeconds      = 1.0
	maxBodyErrorLength    = 200
)

var (
	validIssueKey  = regexp.MustCompile(`^[A-Z][A-Z0-9_]+-\d+$`)
	validCommentID = regexp.MustCompile(`^\d+$`)
)

type Client struct {
	baseURL    string
	authHeader string
	client     *http.Client
	logger     *zap.Logger
	sleepFn    func(time.Duration) <-chan time.Time
}

func NewClient(baseURL, username, apiToken string, logger *zap.Logger) *Client {
	return newClient(baseURL, username, apiToken, &http.Client{}, logger, time.After)
}

func NewClientForTest(baseURL, username, apiToken string, httpClient *http.Client, logger *zap.Logger, sleepFn func(time.Duration) <-chan time.Time) *Client {
	return newClient(baseURL, username, apiToken, httpClient, logger, sleepFn)
}

func newClient(baseURL, username, apiToken string, httpClient *http.Client, logger *zap.Logger, sleepFn func(time.Duration) <-chan time.Time) *Client {
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+apiToken))
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		authHeader: auth,
		client:     httpClient,
		logger:     logger,
		sleepFn:    sleepFn,
	}
}

func (c *Client) doOperation(ctx context.Context, method, url string, bodyReader io.Reader, okStatusCodes ...int) ([]byte, error) {
	c.logger.Debug("Jira API request", zap.String("method", method), zap.String("url", url))

	var requestBody []byte
	if bodyReader != nil {
		var err error
		requestBody, err = io.ReadAll(bodyReader)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		var bodyForRequest io.Reader
		if requestBody != nil {
			bodyForRequest = bytes.NewReader(requestBody)
		}

		req, err := http.NewRequestWithContext(ctx, method, url, bodyForRequest)
		if err != nil {
			return nil, fmt.Errorf("failed to create %s request: %w", method, err)
		}

		req.Header.Set("Authorization", c.authHeader)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to send %s request: %w", method, err)
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("failed to read response body: %w", readErr)
		}

		for _, okCode := range okStatusCodes {
			if resp.StatusCode == okCode {
				return body, nil
			}
		}

		isRetryable := resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusBadGateway ||
			resp.StatusCode == http.StatusServiceUnavailable ||
			resp.StatusCode == http.StatusGatewayTimeout

		if isRetryable && attempt < maxAttempts {
			waitDuration := c.calculateRetryWait(resp, attempt)
			c.logger.Info("Retryable error from Jira, retrying",
				zap.Int("status", resp.StatusCode),
				zap.Int("attempt", attempt),
				zap.Duration("wait", waitDuration))

			select {
			case <-c.sleepFn(waitDuration):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}

		bodyStr := truncateForError(body)
		if !isTextContentType(resp.Header.Get("Content-Type")) {
			bodyStr = fmt.Sprintf("<%d bytes binary>", len(body))
		}
		return nil, fmt.Errorf("failed to %s %s: status_code=%d, body=%s",
			method, url, resp.StatusCode, bodyStr)
	}

	return nil, fmt.Errorf("failed to %s %s after %d attempts", method, url, maxAttempts)
}

func (c *Client) calculateRetryWait(resp *http.Response, attempt int) time.Duration {
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		if parsed, err := strconv.Atoi(retryAfter); err == nil && parsed > 0 {
			seconds := parsed
			if seconds > maxRetryWaitSeconds {
				seconds = maxRetryWaitSeconds
			}
			return time.Duration(seconds) * time.Second
		}
	}

	backoffSeconds := initialBackoffSeconds * (1 << (attempt - 1))
	if backoffSeconds > maxBackoffSeconds {
		backoffSeconds = maxBackoffSeconds
	}
	jitter, err := randomJitter(maxJitterSeconds)
	if err != nil {
		jitter = 0
	}
	return time.Duration(float64(backoffSeconds)+jitter) * time.Second
}

func (c *Client) doGet(ctx context.Context, url string) ([]byte, error) {
	return c.doOperation(ctx, "GET", url, nil, http.StatusOK)
}

func (c *Client) doPut(ctx context.Context, url string, body io.Reader) ([]byte, error) {
	return c.doOperation(ctx, "PUT", url, body, http.StatusNoContent, http.StatusOK)
}

func (c *Client) doPost(ctx context.Context, url string, body io.Reader) ([]byte, error) {
	return c.doOperation(ctx, "POST", url, body, http.StatusNoContent, http.StatusCreated, http.StatusOK)
}

func validateIssueKey(key string) error {
	if !validIssueKey.MatchString(key) {
		return fmt.Errorf("invalid issue key: %q", key)
	}
	return nil
}

func validateCommentID(id string) error {
	if !validCommentID.MatchString(id) {
		return fmt.Errorf("invalid comment ID: %q", id)
	}
	return nil
}

// SearchTickets searches for issues using JQL with optional pagination.
func (c *Client) SearchTickets(ctx context.Context, jql string, maxResults int, nextPageToken string) (*JiraSearchResponse, error) {
	url := fmt.Sprintf("%s/rest/api/3/search/jql", c.baseURL)

	payload := map[string]any{
		"jql":        jql,
		"maxResults": maxResults,
		"fields":     []string{"summary", "description", "status", "issuetype", "project", "components", "labels", "assignee", "created", "updated", "creator", "reporter"},
	}
	if nextPageToken != "" {
		payload["nextPageToken"] = nextPageToken
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	body, err := c.doPost(ctx, url, bytes.NewReader(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("failed to search tickets: %w", err)
	}

	var result JiraSearchResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}
	return &result, nil
}

// GetComments retrieves all comments on a ticket, paginating as needed.
func (c *Client) GetComments(ctx context.Context, key string) ([]JiraComment, error) {
	if err := validateIssueKey(key); err != nil {
		return nil, err
	}

	var all []JiraComment
	startAt := 0
	for {
		url := fmt.Sprintf("%s/rest/api/3/issue/%s/comment?startAt=%d", c.baseURL, key, startAt)

		body, err := c.doGet(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("failed to get comments for %s: %w", key, err)
		}

		var result JiraComments
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&result); err != nil {
			return nil, fmt.Errorf("failed to decode comments: %w", err)
		}

		if result.Comments != nil {
			all = append(all, result.Comments...)
		}

		if startAt+len(result.Comments) >= result.Total {
			break
		}
		startAt += len(result.Comments)
	}

	if all == nil {
		return []JiraComment{}, nil
	}
	return all, nil
}

// AddComment adds a comment to a ticket. Text is converted to ADF.
func (c *Client) AddComment(ctx context.Context, key, comment string) error {
	if err := validateIssueKey(key); err != nil {
		return err
	}

	url := fmt.Sprintf("%s/rest/api/3/issue/%s/comment", c.baseURL, key)

	payload := map[string]any{
		"body": TextToADF(comment),
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal comment payload: %w", err)
	}

	if _, err := c.doPost(ctx, url, bytes.NewReader(jsonPayload)); err != nil {
		return fmt.Errorf("failed to add comment to %s: %w", key, err)
	}
	return nil
}

// UpdateComment replaces the body of an existing comment.
func (c *Client) UpdateComment(ctx context.Context, key, commentID, body string) error {
	if err := validateIssueKey(key); err != nil {
		return err
	}
	if err := validateCommentID(commentID); err != nil {
		return err
	}

	url := fmt.Sprintf("%s/rest/api/3/issue/%s/comment/%s", c.baseURL, key, commentID)

	payload := map[string]any{
		"body": TextToADF(body),
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal comment payload: %w", err)
	}

	if _, err := c.doPut(ctx, url, bytes.NewReader(jsonPayload)); err != nil {
		return fmt.Errorf("failed to update comment %s on %s: %w", commentID, key, err)
	}
	return nil
}

// AddCommentADF adds a comment using a pre-built ADF body.
func (c *Client) AddCommentADF(ctx context.Context, key string, adfBody map[string]any) error {
	if err := validateIssueKey(key); err != nil {
		return err
	}

	url := fmt.Sprintf("%s/rest/api/3/issue/%s/comment", c.baseURL, key)

	payload := map[string]any{"body": adfBody}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal comment payload: %w", err)
	}

	if _, err := c.doPost(ctx, url, bytes.NewReader(jsonPayload)); err != nil {
		return fmt.Errorf("failed to add comment to %s: %w", key, err)
	}
	return nil
}

// UpdateCommentADF replaces a comment body using a pre-built ADF body.
func (c *Client) UpdateCommentADF(ctx context.Context, key, commentID string, adfBody map[string]any) error {
	if err := validateIssueKey(key); err != nil {
		return err
	}
	if err := validateCommentID(commentID); err != nil {
		return err
	}

	url := fmt.Sprintf("%s/rest/api/3/issue/%s/comment/%s", c.baseURL, key, commentID)

	payload := map[string]any{"body": adfBody}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal comment payload: %w", err)
	}

	if _, err := c.doPut(ctx, url, bytes.NewReader(jsonPayload)); err != nil {
		return fmt.Errorf("failed to update comment %s on %s: %w", commentID, key, err)
	}
	return nil
}

// AddLabel adds a label to a ticket.
func (c *Client) AddLabel(ctx context.Context, key, label string) error {
	if err := validateIssueKey(key); err != nil {
		return err
	}

	url := fmt.Sprintf("%s/rest/api/3/issue/%s", c.baseURL, key)

	payload := map[string]any{
		"update": map[string]any{
			"labels": []map[string]string{
				{"add": label},
			},
		},
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal label payload: %w", err)
	}

	if _, err := c.doPut(ctx, url, bytes.NewReader(jsonPayload)); err != nil {
		return fmt.Errorf("failed to add label %q to %s: %w", label, key, err)
	}
	return nil
}

// RemoveLabel removes a label from a ticket.
func (c *Client) RemoveLabel(ctx context.Context, key, label string) error {
	if err := validateIssueKey(key); err != nil {
		return err
	}

	url := fmt.Sprintf("%s/rest/api/3/issue/%s", c.baseURL, key)

	payload := map[string]any{
		"update": map[string]any{
			"labels": []map[string]string{
				{"remove": label},
			},
		},
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal label payload: %w", err)
	}

	if _, err := c.doPut(ctx, url, bytes.NewReader(jsonPayload)); err != nil {
		return fmt.Errorf("failed to remove label %q from %s: %w", label, key, err)
	}
	return nil
}

func truncateForError(body []byte) string {
	s := string(body)
	if len(s) > maxBodyErrorLength {
		return s[:maxBodyErrorLength] + "..."
	}
	return s
}

func isTextContentType(ct string) bool {
	ct = strings.ToLower(ct)
	return ct == "" ||
		strings.HasPrefix(ct, "text/") ||
		strings.HasPrefix(ct, "application/json") ||
		strings.HasPrefix(ct, "application/xml")
}

func randomJitter(maxSeconds float64) (float64, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0, err
	}
	n := binary.BigEndian.Uint64(buf[:])
	return float64(n) / float64(^uint64(0)) * maxSeconds, nil
}
