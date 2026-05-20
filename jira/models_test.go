package jira

import (
	"encoding/json"
	"testing"
)

func TestADFText_UnmarshalString(t *testing.T) {
	var a ADFText
	if err := json.Unmarshal([]byte(`"hello world"`), &a); err != nil {
		t.Fatal(err)
	}
	if string(a) != "hello world" {
		t.Errorf("got %q, want %q", string(a), "hello world")
	}
}

func TestADFText_UnmarshalNull(t *testing.T) {
	var a ADFText
	if err := json.Unmarshal([]byte(`null`), &a); err != nil {
		t.Fatal(err)
	}
	if string(a) != "" {
		t.Errorf("got %q, want empty", string(a))
	}
}

func TestADFText_UnmarshalADFObject(t *testing.T) {
	adf := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"hello"}]},{"type":"paragraph","content":[{"type":"text","text":"world"}]}]}`
	var a ADFText
	if err := json.Unmarshal([]byte(adf), &a); err != nil {
		t.Fatal(err)
	}
	if string(a) != "hello\nworld" {
		t.Errorf("got %q, want %q", string(a), "hello\nworld")
	}
}

func TestADFText_HardBreak(t *testing.T) {
	adf := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"line1"},{"type":"hardBreak"},{"type":"text","text":"line2"}]}]}`
	var a ADFText
	if err := json.Unmarshal([]byte(adf), &a); err != nil {
		t.Fatal(err)
	}
	if string(a) != "line1\nline2" {
		t.Errorf("got %q", string(a))
	}
}

func TestTextToADF_Basic(t *testing.T) {
	result := TextToADF("hello\nworld")
	if result["type"] != "doc" {
		t.Errorf("type = %v, want doc", result["type"])
	}
	content := result["content"].([]map[string]any)
	if len(content) != 2 {
		t.Fatalf("content length = %d, want 2", len(content))
	}
}

func TestTextToADF_EmptyLine(t *testing.T) {
	result := TextToADF("hello\n\nworld")
	content := result["content"].([]map[string]any)
	if len(content) != 3 {
		t.Fatalf("content length = %d, want 3", len(content))
	}
	middle := content[1]["content"].([]map[string]any)
	if len(middle) != 0 {
		t.Errorf("empty paragraph should have no content")
	}
}

func TestTextToADF_CRLFNormalized(t *testing.T) {
	result := TextToADF("hello\r\nworld")
	content := result["content"].([]map[string]any)
	if len(content) != 2 {
		t.Errorf("CRLF should produce 2 paragraphs, got %d", len(content))
	}
}

func TestJiraTime_JiraFormat(t *testing.T) {
	var jt JiraTime
	if err := json.Unmarshal([]byte(`"2026-05-20T10:30:00.000+0000"`), &jt); err != nil {
		t.Fatal(err)
	}
	if jt.IsZero() {
		t.Error("expected non-zero time")
	}
}

func TestJiraTime_RFC3339Z(t *testing.T) {
	var jt JiraTime
	if err := json.Unmarshal([]byte(`"2026-05-20T10:30:00.000Z"`), &jt); err != nil {
		t.Fatal(err)
	}
	if jt.IsZero() {
		t.Error("expected non-zero time")
	}
}

func TestJiraTime_Null(t *testing.T) {
	var jt JiraTime
	if err := json.Unmarshal([]byte(`null`), &jt); err != nil {
		t.Fatal(err)
	}
	if !jt.IsZero() {
		t.Error("expected zero time for null")
	}
}

func TestJiraTime_EmptyString(t *testing.T) {
	var jt JiraTime
	if err := json.Unmarshal([]byte(`""`), &jt); err != nil {
		t.Fatal(err)
	}
	if !jt.IsZero() {
		t.Error("expected zero time for empty string")
	}
}
