package triage

import (
	"encoding/json"
	"strings"
	"testing"

	"triage-bot/config"
	"triage-bot/jira"
)

func TestComputeHash(t *testing.T) {
	h := computeHash("hello world")
	if len(h) != hashLen {
		t.Errorf("hash length = %d, want %d", len(h), hashLen)
	}

	h2 := computeHash("hello world")
	if h != h2 {
		t.Error("same input produced different hashes")
	}

	h3 := computeHash("different")
	if h == h3 {
		t.Error("different inputs produced same hash")
	}
}

func TestAppendHashFooter(t *testing.T) {
	body := "Assessment text here"
	result := appendHashFooter(body, "abc123def456")

	if got := extractHash(result); got != "abc123def456" {
		t.Errorf("extractHash roundtrip = %q, want %q", got, "abc123def456")
	}

	expected := "Assessment text here\n\n---\n_triage-bot | desc:abc123def456_\n"
	if result != expected {
		t.Errorf("appendHashFooter =\n%q\nwant\n%q", result, expected)
	}
}

func TestExtractHash(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "standard footer",
			body: "some text\n---\n_triage-bot | desc:abc123def456_\n",
			want: "abc123def456",
		},
		{
			name: "no footer",
			body: "just regular text",
			want: "",
		},
		{
			name: "hash at end without trailing underscore",
			body: "text\n_triage-bot | desc:abc123def456",
			want: "abc123def456",
		},
		{
			name: "wrong length hash rejected",
			body: "text\n_triage-bot | desc:abc123_",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractHash(tt.body)
			if got != tt.want {
				t.Errorf("extractHash = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindBotComment(t *testing.T) {
	p := &Processor{
		cfg: testConfig("bot@example.com"),
	}

	comments := []jira.JiraComment{
		{
			ID:     "100",
			Body:   "human comment",
			Author: jira.JiraUser{EmailAddress: "user@example.com"},
		},
		{
			ID:     "200",
			Body:   jira.ADFText("triage report\n---\n_triage-bot | desc:abc123def456_\n"),
			Author: jira.JiraUser{EmailAddress: "bot@example.com"},
		},
		{
			ID:     "300",
			Body:   "another human comment",
			Author: jira.JiraUser{EmailAddress: "user@example.com"},
		},
	}

	found := p.findBotComment(comments)
	if found == nil {
		t.Fatal("expected to find bot comment")
	}
	if found.ID != "200" {
		t.Errorf("found comment ID = %q, want %q", found.ID, "200")
	}
}

func TestFindBotComment_NoMatch(t *testing.T) {
	p := &Processor{
		cfg: testConfig("bot@example.com"),
	}

	comments := []jira.JiraComment{
		{
			ID:     "100",
			Body:   "just a comment",
			Author: jira.JiraUser{EmailAddress: "user@example.com"},
		},
	}

	found := p.findBotComment(comments)
	if found != nil {
		t.Error("expected nil, got a comment")
	}
}

func TestFindBotComment_BotWithoutMarker(t *testing.T) {
	p := &Processor{
		cfg: testConfig("bot@example.com"),
	}

	comments := []jira.JiraComment{
		{
			ID:     "100",
			Body:   "bot comment without hash marker",
			Author: jira.JiraUser{EmailAddress: "bot@example.com"},
		},
	}

	found := p.findBotComment(comments)
	if found != nil {
		t.Error("expected nil for bot comment without marker")
	}
}

func TestBuildADFComment_Valid(t *testing.T) {
	adf := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"hello"}]}]}`
	result, err := buildADFComment(adf, "abc123def456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := result["content"].([]any)
	// original paragraph + rule + hash footer = 3 nodes
	if len(content) != 3 {
		t.Fatalf("content length = %d, want 3", len(content))
	}

	rule := content[1].(map[string]any)
	if rule["type"] != "rule" {
		t.Errorf("second node type = %q, want 'rule'", rule["type"])
	}

	footer := content[2].(map[string]any)
	footerContent := footer["content"].([]any)
	textNode := footerContent[0].(map[string]any)
	if got := textNode["text"].(string); got != "triage-bot | desc:abc123def456" {
		t.Errorf("footer text = %q, want %q", got, "triage-bot | desc:abc123def456")
	}
}

func TestBuildADFComment_InvalidJSON(t *testing.T) {
	_, err := buildADFComment("not json", "abc123")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestBuildADFComment_MissingDocType(t *testing.T) {
	_, err := buildADFComment(`{"type":"paragraph","content":[]}`, "abc123")
	if err == nil {
		t.Error("expected error for missing doc type")
	}
}

func TestADFHashRoundtrip(t *testing.T) {
	assessment := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"hello"}]}]}`
	hash := "abc123def456"

	adf, err := buildADFComment(assessment, hash)
	if err != nil {
		t.Fatalf("buildADFComment failed: %v", err)
	}

	// Simulate what Jira does: marshal to JSON, then unmarshal via ADFText
	adfJSON, err := json.Marshal(adf)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var extracted jira.ADFText
	if err := json.Unmarshal(adfJSON, &extracted); err != nil {
		t.Fatalf("ADFText unmarshal failed: %v", err)
	}

	plainText := string(extracted)

	if !strings.Contains(plainText, hashPrefix) {
		t.Errorf("plain text does not contain hash prefix %q:\n%s", hashPrefix, plainText)
	}

	got := extractHash(plainText)
	if got != hash {
		t.Errorf("roundtrip extractHash = %q, want %q\nplain text:\n%s", got, hash, plainText)
	}
}

func testConfig(botUsername string) config.Config {
	return config.Config{
		Jira: config.JiraConfig{
			BotUsername: botUsername,
		},
	}
}
