package jira

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	maxRetries              = 2
	maxRetryWaitSeconds     = 60
	initialBackoffSeconds   = 1
	maxBackoffSeconds       = 16
	maxJitterSeconds        = 1.0
	maxBodyLogLength        = 500
	maxBodyErrorLength      = 200
)

type Client struct {
	baseURL  string
	username string
	apiToken string
	client   *http.Client
	logger   *zap.Logger
	sleepFn  func(time.Duration) <-chan time.Time
}

func NewClient(baseURL, username, apiToken string, logger *zap.Logger) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		apiToken: apiToken,
		client:   &http.Client{},
		logger:   logger,
		sleepFn:  time.After,
	}
}

func NewClientForTest(baseURL, username, apiToken string, httpClient *http.Client, logger *zap.Logger, sleepFn func(time.Duration) <-chan time.Time) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		apiToken: apiToken,
		client:   httpClient,
		logger:   logger,
		sleepFn:  sleepFn,
	}
}

func (c *Client) doOperation(method, url string, bodyReader io.Reader, okStatusCodes ...int) ([]byte, error) {
	c.logger.Debug("Jira API request", zap.String("method", method), zap.String("url", url))

	var requestBody []byte
	if bodyReader != nil {
		var err error
		requestBody, err = io.ReadAll(bodyReader)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		var bodyForRequest io.Reader
		if requestBody != nil {
			bodyForRequest = bytes.NewReader(requestBody)
		}

		req, err := http.NewRequest(method, url, bodyForRequest)
		if err != nil {
			return nil, fmt.Errorf("failed to create %s request: %w", method, err)
		}

		credentials := base64.StdEncoding.EncodeToString(
			[]byte(c.username + ":" + c.apiToken))
		req.Header.Set("Authorization", "Basic "+credentials)
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

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries {
			waitDuration := c.calculateRetryWait(resp, attempt)
			c.logger.Info("Rate limited by Jira, retrying",
				zap.Int("attempt", attempt),
				zap.Duration("wait", waitDuration))
			<-c.sleepFn(waitDuration)
			continue
		}

		bodyStr := truncateForError(body)
		if !isTextContentType(resp.Header.Get("Content-Type")) {
			bodyStr = fmt.Sprintf("<%d bytes binary>", len(body))
		}
		return nil, fmt.Errorf("failed to %s %s: status_code=%d, body=%s",
			method, url, resp.StatusCode, bodyStr)
	}

	return nil, fmt.Errorf("failed to %s %s after %d retries", method, url, maxRetries)
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

func (c *Client) doGet(url string) ([]byte, error) {
	return c.doOperation("GET", url, nil, http.StatusOK)
}

func (c *Client) doPut(url string, body io.Reader) ([]byte, error) {
	return c.doOperation("PUT", url, body, http.StatusNoContent, http.StatusOK)
}

func (c *Client) doPost(url string, body io.Reader) ([]byte, error) {
	return c.doOperation("POST", url, body, http.StatusNoContent, http.StatusCreated, http.StatusOK)
}

// SearchTickets searches for issues using JQL.
func (c *Client) SearchTickets(jql string, maxResults int) (*JiraSearchResponse, error) {
	url := fmt.Sprintf("%s/rest/api/3/search/jql", c.baseURL)

	payload := map[string]any{
		"jql":        jql,
		"maxResults": maxResults,
		"fields":     []string{"summary", "description", "status", "issuetype", "project", "components", "labels", "assignee", "created", "updated", "creator", "reporter"},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	body, err := c.doPost(url, bytes.NewReader(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("failed to search tickets: %w", err)
	}

	var result JiraSearchResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}
	return &result, nil
}

// GetComments retrieves all comments on a ticket.
func (c *Client) GetComments(key string) ([]JiraComment, error) {
	url := fmt.Sprintf("%s/rest/api/3/issue/%s/comment", c.baseURL, key)

	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get comments for %s: %w", key, err)
	}

	var result JiraComments
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode comments: %w", err)
	}

	if result.Comments == nil {
		return []JiraComment{}, nil
	}
	return result.Comments, nil
}

// AddComment adds a comment to a ticket. Text is converted to ADF.
func (c *Client) AddComment(key, comment string) error {
	url := fmt.Sprintf("%s/rest/api/3/issue/%s/comment", c.baseURL, key)

	payload := map[string]any{
		"body": TextToADF(comment),
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal comment payload: %w", err)
	}

	if _, err := c.doPost(url, bytes.NewReader(jsonPayload)); err != nil {
		return fmt.Errorf("failed to add comment to %s: %w", key, err)
	}
	return nil
}

// UpdateComment replaces the body of an existing comment.
func (c *Client) UpdateComment(key, commentID, body string) error {
	url := fmt.Sprintf("%s/rest/api/3/issue/%s/comment/%s", c.baseURL, key, commentID)

	payload := map[string]any{
		"body": TextToADF(body),
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal comment payload: %w", err)
	}

	if _, err := c.doPut(url, bytes.NewReader(jsonPayload)); err != nil {
		return fmt.Errorf("failed to update comment %s on %s: %w", commentID, key, err)
	}
	return nil
}

// AddCommentADF adds a comment using a pre-built ADF body.
func (c *Client) AddCommentADF(key string, adfBody map[string]any) error {
	url := fmt.Sprintf("%s/rest/api/3/issue/%s/comment", c.baseURL, key)

	payload := map[string]any{"body": adfBody}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal comment payload: %w", err)
	}

	if _, err := c.doPost(url, bytes.NewReader(jsonPayload)); err != nil {
		return fmt.Errorf("failed to add comment to %s: %w", key, err)
	}
	return nil
}

// UpdateCommentADF replaces a comment body using a pre-built ADF body.
func (c *Client) UpdateCommentADF(key, commentID string, adfBody map[string]any) error {
	url := fmt.Sprintf("%s/rest/api/3/issue/%s/comment/%s", c.baseURL, key, commentID)

	payload := map[string]any{"body": adfBody}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal comment payload: %w", err)
	}

	if _, err := c.doPut(url, bytes.NewReader(jsonPayload)); err != nil {
		return fmt.Errorf("failed to update comment %s on %s: %w", commentID, key, err)
	}
	return nil
}

// AddLabel adds a label to a ticket.
func (c *Client) AddLabel(key, label string) error {
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

	if _, err := c.doPut(url, bytes.NewReader(jsonPayload)); err != nil {
		return fmt.Errorf("failed to add label %q to %s: %w", label, key, err)
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
