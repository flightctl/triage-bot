package jira

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func testLogger() *zap.Logger {
	l, _ := zap.NewDevelopmentConfig().Build()
	return l
}

func instantSleep(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- time.Now()
	return ch
}

func TestDoOperation_RetriesOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c := NewClientForTest(srv.URL, "user", "token", srv.Client(), testLogger(), instantSleep)
	body, err := c.doGet(context.Background(), srv.URL+"/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", string(body))
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("calls = %d, want 2", atomic.LoadInt32(&calls))
	}
}

func TestDoOperation_RetriesOn503(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `ok`)
	}))
	defer srv.Close()

	c := NewClientForTest(srv.URL, "user", "token", srv.Client(), testLogger(), instantSleep)
	_, err := c.doGet(context.Background(), srv.URL+"/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("calls = %d, want 2", atomic.LoadInt32(&calls))
	}
}

func TestDoOperation_NoRetryOn400(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "bad request")
	}))
	defer srv.Close()

	c := NewClientForTest(srv.URL, "user", "token", srv.Client(), testLogger(), instantSleep)
	_, err := c.doGet(context.Background(), srv.URL+"/test")
	if err == nil {
		t.Fatal("expected error")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("calls = %d, want 1 (no retry on 400)", atomic.LoadInt32(&calls))
	}
}

func TestDoOperation_RespectsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	c := NewClientForTest(srv.URL, "user", "token", srv.Client(), testLogger(), instantSleep)
	_, err := c.doGet(ctx, srv.URL+"/test")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestValidateIssueKey(t *testing.T) {
	tests := []struct {
		key   string
		valid bool
	}{
		{"PROJ-123", true},
		{"OSAC-915", true},
		{"AB-1", true},
		{"A-1", false},
		{"proj-123", false},
		{"PROJ", false},
		{"PROJ-", false},
		{"PROJ-abc", false},
		{"../../admin", false},
		{"", false},
	}
	for _, tt := range tests {
		err := validateIssueKey(tt.key)
		if (err == nil) != tt.valid {
			t.Errorf("validateIssueKey(%q) valid=%v, want %v", tt.key, err == nil, tt.valid)
		}
	}
}

func TestValidateCommentID(t *testing.T) {
	tests := []struct {
		id    string
		valid bool
	}{
		{"12345", true},
		{"1", true},
		{"abc", false},
		{"12.34", false},
		{"", false},
	}
	for _, tt := range tests {
		err := validateCommentID(tt.id)
		if (err == nil) != tt.valid {
			t.Errorf("validateCommentID(%q) valid=%v, want %v", tt.id, err == nil, tt.valid)
		}
	}
}

func TestGetComments_Paginates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("startAt") == "0" || r.URL.Query().Get("startAt") == "" {
			fmt.Fprint(w, `{"comments":[{"id":"1","body":"first"}],"maxResults":1,"total":2,"startAt":0}`)
		} else {
			fmt.Fprint(w, `{"comments":[{"id":"2","body":"second"}],"maxResults":1,"total":2,"startAt":1}`)
		}
	}))
	defer srv.Close()

	c := NewClientForTest(srv.URL, "user", "token", srv.Client(), testLogger(), instantSleep)
	comments, err := c.GetComments(context.Background(), "PROJ-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 2 {
		t.Errorf("got %d comments, want 2", len(comments))
	}
}

func TestCredentialsPrecomputed(t *testing.T) {
	c := NewClient("http://example.com", "user", "token", testLogger())
	if c.authHeader == "" {
		t.Error("authHeader not precomputed")
	}
	if c.authHeader != "Basic dXNlcjp0b2tlbg==" {
		t.Errorf("authHeader = %q, want Basic dXNlcjp0b2tlbg==", c.authHeader)
	}
}
