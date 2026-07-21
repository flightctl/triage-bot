package triage

import (
	"context"
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

	expected := "Assessment text here\n\n---\n_triage-bot | v:3 | desc:abc123def456_\n"
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
	md := "# Heading\n\nSome assessment text."
	result, err := buildADFComment(md, "abc123def456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := result["content"].([]any)
	// heading + paragraph + rule + hash footer = 4 nodes
	if len(content) != 4 {
		t.Fatalf("content length = %d, want 4", len(content))
	}

	heading := content[0].(map[string]any)
	if heading["type"] != "heading" {
		t.Errorf("first node type = %q, want 'heading'", heading["type"])
	}

	para := content[1].(map[string]any)
	if para["type"] != "paragraph" {
		t.Errorf("second node type = %q, want 'paragraph'", para["type"])
	}

	rule := content[2].(map[string]any)
	if rule["type"] != "rule" {
		t.Errorf("third node type = %q, want 'rule'", rule["type"])
	}

	footer := content[3].(map[string]any)
	footerContent := footer["content"].([]any)
	textNode := footerContent[0].(map[string]any)
	if got := textNode["text"].(string); got != "triage-bot | v:3 | desc:abc123def456" {
		t.Errorf("footer text = %q, want %q", got, "triage-bot | v:3 | desc:abc123def456")
	}
}

func TestBuildADFComment_EmptyInput(t *testing.T) {
	_, err := buildADFComment("", "abc123def456")
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestBuildADFComment_WhitespaceOnly(t *testing.T) {
	_, err := buildADFComment("   \n\n   ", "abc123def456")
	if err == nil {
		t.Error("expected error for whitespace-only input")
	}
}

func TestBuildADFComment_BOM(t *testing.T) {
	md := "\uFEFF# Test\n\nSome text."
	result, err := buildADFComment(md, "abc123def456")
	if err != nil {
		t.Fatalf("unexpected error for BOM-prefixed markdown: %v", err)
	}
	if result["type"] != "doc" {
		t.Errorf("type = %q, want 'doc'", result["type"])
	}
}

func TestTrimInvisible(t *testing.T) {
	raw := "# Assessment"

	bom := "\uFEFF"
	zws := "\u200B"
	zwnj := "\u200C"
	zwj := "\u200D"
	wj := "\u2060"
	nbsp := " "

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"clean input", raw, raw},
		{"leading BOM", bom + raw, raw},
		{"trailing BOM", raw + bom, raw},
		{"surrounding BOM", bom + raw + bom, raw},
		{"zero-width space", zws + raw + zws, raw},
		{"zero-width non-joiner", zwnj + raw, raw},
		{"zero-width joiner", zwj + raw, raw},
		{"word joiner", wj + raw, raw},
		{"no-break space", nbsp + raw + nbsp, raw},
		{"mixed invisible", bom + zws + nbsp + raw + nbsp + zwnj + bom, raw},
		{"whitespace and BOM", "  " + bom + "\n" + raw + "\n" + bom + "  ", raw},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trimInvisible(tt.input)
			if got != tt.want {
				t.Errorf("trimInvisible() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestADFHashRoundtrip(t *testing.T) {
	assessment := "# Assessment\n\nSome analysis text."
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

func TestExtractVersion(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "new format",
			body: "text\n---\n_triage-bot | v:3 | desc:abc123def456_\n",
			want: "3",
		},
		{
			name: "legacy format (no version)",
			body: "text\n---\n_triage-bot | desc:abc123def456_\n",
			want: "",
		},
		{
			name: "no marker at all",
			body: "just text",
			want: "",
		},
		{
			name: "higher version",
			body: "text\n_triage-bot | v:15 | desc:abc123def456_",
			want: "15",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractVersion(tt.body)
			if got != tt.want {
				t.Errorf("extractVersion = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractHash_NewFormat(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "new format with version",
			body: "text\n---\n_triage-bot | v:3 | desc:abc123def456_\n",
			want: "abc123def456",
		},
		{
			name: "legacy format still works",
			body: "text\n---\n_triage-bot | desc:abc123def456_\n",
			want: "abc123def456",
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

func TestDetermineAction_VersionMismatch(t *testing.T) {
	tests := []struct {
		name        string
		commentBody string
		descHash    string
		wantAction  Action
	}{
		{
			name:        "current version and matching hash skips",
			commentBody: "text\n_triage-bot | v:" + assessmentVersion + " | desc:abc123def456_",
			descHash:    "abc123def456",
			wantAction:  ActionSkip,
		},
		{
			name:        "old version triggers update even with matching hash",
			commentBody: "text\n_triage-bot | v:1 | desc:abc123def456_",
			descHash:    "abc123def456",
			wantAction:  ActionUpdate,
		},
		{
			name:        "legacy format (no version) triggers update",
			commentBody: "text\n_triage-bot | desc:abc123def456_",
			descHash:    "abc123def456",
			wantAction:  ActionUpdate,
		},
		{
			name:        "changed description triggers update",
			commentBody: "text\n_triage-bot | v:" + assessmentVersion + " | desc:abc123def456_",
			descHash:    "different1234",
			wantAction:  ActionUpdate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Processor{
				cfg: testConfig("bot@example.com"),
				jira: &stubJiraClient{
					comments: []jira.JiraComment{
						{
							ID:     "1",
							Body:   jira.ADFText(tt.commentBody),
							Author: jira.JiraUser{EmailAddress: "bot@example.com"},
						},
					},
				},
			}

			action, _ := p.determineAction(context.Background(), "PROJ-1", tt.descHash)
			if action != tt.wantAction {
				t.Errorf("action = %s, want %s", action, tt.wantAction)
			}
		})
	}
}

type stubJiraClient struct {
	comments []jira.JiraComment
}

func (s *stubJiraClient) GetComments(_ context.Context, _ string) ([]jira.JiraComment, error) {
	return s.comments, nil
}
func (s *stubJiraClient) AddComment(_ context.Context, _, _ string) error       { return nil }
func (s *stubJiraClient) UpdateComment(_ context.Context, _, _, _ string) error { return nil }
func (s *stubJiraClient) AddCommentADF(_ context.Context, _ string, _ map[string]any) error {
	return nil
}
func (s *stubJiraClient) UpdateCommentADF(_ context.Context, _, _ string, _ map[string]any) error {
	return nil
}
func (s *stubJiraClient) AddLabel(_ context.Context, _, _ string) error    { return nil }
func (s *stubJiraClient) RemoveLabel(_ context.Context, _, _ string) error { return nil }

func testConfig(botUsername string) config.Config {
	return config.Config{
		Jira: config.JiraConfig{
			BotUsername: botUsername,
		},
	}
}
