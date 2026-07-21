package jira

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarkdownToADF_Empty(t *testing.T) {
	_, err := MarkdownToADF("")
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestMarkdownToADF_WhitespaceOnly(t *testing.T) {
	_, err := MarkdownToADF("   \n\n  \t  ")
	if err == nil {
		t.Error("expected error for whitespace-only input")
	}
}

func TestMarkdownToADF_SimpleParagraph(t *testing.T) {
	adf, err := MarkdownToADF("Hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertDocType(t, adf)
	content := docContent(t, adf)
	if len(content) != 1 {
		t.Fatalf("content length = %d, want 1", len(content))
	}
	assertNodeType(t, content[0], "paragraph")

	text := extractTextFromNode(content[0])
	if text != "Hello world" {
		t.Errorf("text = %q, want %q", text, "Hello world")
	}
}

func TestMarkdownToADF_MultipleParagraphs(t *testing.T) {
	adf, err := MarkdownToADF("First paragraph\n\nSecond paragraph")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	if len(content) != 2 {
		t.Fatalf("content length = %d, want 2", len(content))
	}
	assertNodeType(t, content[0], "paragraph")
	assertNodeType(t, content[1], "paragraph")
}

func TestMarkdownToADF_Headings(t *testing.T) {
	tests := []struct {
		md    string
		level int
	}{
		{"# H1", 1},
		{"## H2", 2},
		{"### H3", 3},
		{"#### H4", 4},
		{"##### H5", 5},
		{"###### H6", 6},
	}

	for _, tt := range tests {
		t.Run(tt.md, func(t *testing.T) {
			adf, err := MarkdownToADF(tt.md)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			content := docContent(t, adf)
			if len(content) < 1 {
				t.Fatal("no content nodes")
			}
			assertNodeType(t, content[0], "heading")

			attrs := content[0].(map[string]any)["attrs"].(map[string]any)
			if got := attrs["level"].(int); got != tt.level {
				t.Errorf("heading level = %d, want %d", got, tt.level)
			}
		})
	}
}

func TestMarkdownToADF_Bold(t *testing.T) {
	adf, err := MarkdownToADF("This is **bold** text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	para := content[0].(map[string]any)
	inlines := para["content"].([]any)

	found := false
	for _, inline := range inlines {
		node := inline.(map[string]any)
		if node["text"] == "bold" {
			found = true
			assertHasMark(t, node, "strong")
		}
	}
	if !found {
		t.Error("did not find 'bold' text node")
	}
}

func TestMarkdownToADF_Italic(t *testing.T) {
	adf, err := MarkdownToADF("This is *italic* text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	para := content[0].(map[string]any)
	inlines := para["content"].([]any)

	found := false
	for _, inline := range inlines {
		node := inline.(map[string]any)
		if node["text"] == "italic" {
			found = true
			assertHasMark(t, node, "em")
		}
	}
	if !found {
		t.Error("did not find 'italic' text node")
	}
}

func TestMarkdownToADF_BoldItalic(t *testing.T) {
	adf, err := MarkdownToADF("This is ***both*** text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	para := content[0].(map[string]any)
	inlines := para["content"].([]any)

	found := false
	for _, inline := range inlines {
		node := inline.(map[string]any)
		text, _ := node["text"].(string)
		if text == "both" {
			found = true
			assertHasMark(t, node, "strong")
			assertHasMark(t, node, "em")
		}
	}
	if !found {
		t.Error("did not find 'both' text node")
	}
}

func TestMarkdownToADF_InlineCode(t *testing.T) {
	adf, err := MarkdownToADF("Use `fmt.Println` here")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	para := content[0].(map[string]any)
	inlines := para["content"].([]any)

	found := false
	for _, inline := range inlines {
		node := inline.(map[string]any)
		if node["text"] == "fmt.Println" {
			found = true
			assertHasMark(t, node, "code")
		}
	}
	if !found {
		t.Error("did not find 'fmt.Println' text node")
	}
}

func TestMarkdownToADF_CodeBlockWithLanguage(t *testing.T) {
	md := "```go\nfmt.Println(\"hello\")\n```"
	adf, err := MarkdownToADF(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	if len(content) < 1 {
		t.Fatal("no content nodes")
	}
	assertNodeType(t, content[0], "codeBlock")

	node := content[0].(map[string]any)
	attrs, ok := node["attrs"].(map[string]any)
	if !ok {
		t.Fatal("codeBlock missing attrs")
	}
	if attrs["language"] != "go" {
		t.Errorf("language = %q, want %q", attrs["language"], "go")
	}

	text := extractTextFromNode(node)
	if !strings.Contains(text, "fmt.Println") {
		t.Errorf("code block text = %q, expected to contain fmt.Println", text)
	}
}

func TestMarkdownToADF_CodeBlockWithoutLanguage(t *testing.T) {
	md := "```\nsome code\n```"
	adf, err := MarkdownToADF(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	assertNodeType(t, content[0], "codeBlock")

	node := content[0].(map[string]any)
	if _, ok := node["attrs"]; ok {
		t.Error("codeBlock without language should not have attrs")
	}
}

func TestMarkdownToADF_Link(t *testing.T) {
	adf, err := MarkdownToADF("[click here](https://example.com)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	para := content[0].(map[string]any)
	inlines := para["content"].([]any)

	found := false
	for _, inline := range inlines {
		node := inline.(map[string]any)
		if node["text"] == "click here" {
			found = true
			marks := node["marks"].([]any)
			linkFound := false
			for _, m := range marks {
				mark := m.(map[string]any)
				if mark["type"] == "link" {
					linkFound = true
					attrs := mark["attrs"].(map[string]any)
					if attrs["href"] != "https://example.com" {
						t.Errorf("link href = %q, want %q", attrs["href"], "https://example.com")
					}
				}
			}
			if !linkFound {
				t.Error("link mark not found")
			}
		}
	}
	if !found {
		t.Error("did not find 'click here' text node")
	}
}

func TestMarkdownToADF_BulletList(t *testing.T) {
	md := "- Item one\n- Item two\n- Item three"
	adf, err := MarkdownToADF(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	if len(content) < 1 {
		t.Fatal("no content nodes")
	}
	assertNodeType(t, content[0], "bulletList")

	list := content[0].(map[string]any)
	items := list["content"].([]any)
	if len(items) != 3 {
		t.Fatalf("list items = %d, want 3", len(items))
	}
	for _, item := range items {
		assertNodeType(t, item, "listItem")
		itemContent := item.(map[string]any)["content"].([]any)
		if len(itemContent) < 1 {
			t.Fatal("listItem has no content")
		}
		assertNodeType(t, itemContent[0], "paragraph")
	}
}

func TestMarkdownToADF_OrderedList(t *testing.T) {
	md := "1. First\n2. Second\n3. Third"
	adf, err := MarkdownToADF(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	assertNodeType(t, content[0], "orderedList")

	list := content[0].(map[string]any)
	items := list["content"].([]any)
	if len(items) != 3 {
		t.Fatalf("list items = %d, want 3", len(items))
	}
	for _, item := range items {
		assertNodeType(t, item, "listItem")
		itemContent := item.(map[string]any)["content"].([]any)
		if len(itemContent) < 1 {
			t.Fatal("listItem has no content")
		}
		assertNodeType(t, itemContent[0], "paragraph")
	}
}

func TestMarkdownToADF_NestedList(t *testing.T) {
	md := "- Parent\n  - Child\n  - Child 2\n- Parent 2"
	adf, err := MarkdownToADF(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	assertNodeType(t, content[0], "bulletList")

	list := content[0].(map[string]any)
	items := list["content"].([]any)
	if len(items) != 2 {
		t.Fatalf("top-level items = %d, want 2", len(items))
	}

	firstItem := items[0].(map[string]any)
	firstItemContent := firstItem["content"].([]any)
	nestedListFound := false
	for _, child := range firstItemContent {
		node := child.(map[string]any)
		if node["type"] == "bulletList" {
			nestedListFound = true
			nestedItems := node["content"].([]any)
			if len(nestedItems) != 2 {
				t.Errorf("nested items = %d, want 2", len(nestedItems))
			}
		}
	}
	if !nestedListFound {
		t.Error("expected nested bulletList in first item")
	}
}

func TestMarkdownToADF_Table(t *testing.T) {
	md := "| Col1 | Col2 |\n| --- | --- |\n| A | B |\n| C | D |"
	adf, err := MarkdownToADF(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	if len(content) < 1 {
		t.Fatal("no content nodes")
	}
	assertNodeType(t, content[0], "table")

	table := content[0].(map[string]any)
	rows := table["content"].([]any)
	if len(rows) != 3 {
		t.Fatalf("table rows = %d, want 3 (1 header + 2 data)", len(rows))
	}

	// Header row should contain tableHeader cells
	headerRow := rows[0].(map[string]any)
	assertNodeType(t, headerRow, "tableRow")
	headerCells := headerRow["content"].([]any)
	if len(headerCells) != 2 {
		t.Fatalf("header cells = %d, want 2", len(headerCells))
	}
	assertNodeType(t, headerCells[0], "tableHeader")

	// Data rows should contain tableCell cells
	dataRow := rows[1].(map[string]any)
	dataCells := dataRow["content"].([]any)
	assertNodeType(t, dataCells[0], "tableCell")
}

func TestMarkdownToADF_HorizontalRule(t *testing.T) {
	adf, err := MarkdownToADF("Above\n\n---\n\nBelow")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	ruleFound := false
	for _, node := range content {
		n := node.(map[string]any)
		if n["type"] == "rule" {
			ruleFound = true
		}
	}
	if !ruleFound {
		t.Error("expected a rule node")
	}
}

func TestMarkdownToADF_Blockquote(t *testing.T) {
	adf, err := MarkdownToADF("> This is a quote")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	assertNodeType(t, content[0], "blockquote")
}

func TestMarkdownToADF_MixedContent(t *testing.T) {
	md := `# Title

Some **bold** and *italic* text with ` + "`code`" + `.

- Item one
- Item two

` + "```go\nfunc main() {}\n```" + `

---

> A quote
`
	adf, err := MarkdownToADF(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)

	expectedTypes := []string{
		"heading",
		"paragraph",
		"bulletList",
		"codeBlock",
		"rule",
		"blockquote",
	}

	if len(content) < len(expectedTypes) {
		t.Fatalf("content nodes = %d, want at least %d", len(content), len(expectedTypes))
	}

	for i, want := range expectedTypes {
		assertNodeType(t, content[i], want)
	}
}

func TestMarkdownToADF_UnescapedQuotesInText(t *testing.T) {
	// This is the exact failure mode that motivated the change from ADF JSON.
	// Markdown handles embedded quotes naturally — no escaping needed.
	md := `## Source Analysis

The step "Build and load component image" clones the PR branch.`

	adf, err := MarkdownToADF(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the text with embedded quotes survived the conversion.
	adfJSON, err := json.Marshal(adf)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// The JSON must be valid (this is the whole point).
	var reparsed map[string]any
	if err := json.Unmarshal(adfJSON, &reparsed); err != nil {
		t.Fatalf("round-trip JSON is invalid: %v\n%s", err, adfJSON)
	}

	// Verify the quoted text is preserved.
	text := extractAllText(adf)
	if !strings.Contains(text, `"Build and load component image"`) {
		t.Errorf("quoted text not preserved in ADF:\n%s", text)
	}
}

func TestMarkdownToADF_TableWithFormatting(t *testing.T) {
	md := "| Name | Status |\n| --- | --- |\n| **Alpha** | *active* |\n| `Beta` | done |"
	adf, err := MarkdownToADF(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	assertNodeType(t, content[0], "table")

	table := content[0].(map[string]any)
	rows := table["content"].([]any)
	if len(rows) != 3 {
		t.Fatalf("table rows = %d, want 3", len(rows))
	}

	// Data row 1: **Alpha** should have strong mark
	dataRow1 := rows[1].(map[string]any)
	cells := dataRow1["content"].([]any)
	cell0 := cells[0].(map[string]any)
	cellContent := cell0["content"].([]any)
	para := cellContent[0].(map[string]any)
	inlines := para["content"].([]any)
	boldFound := false
	for _, inline := range inlines {
		node := inline.(map[string]any)
		if node["text"] == "Alpha" {
			boldFound = true
			assertHasMark(t, node, "strong")
		}
	}
	if !boldFound {
		t.Error("did not find bold 'Alpha' in table cell")
	}
}

func TestMarkdownToADF_SoftLineBreak(t *testing.T) {
	md := "Line one\nLine two"
	adf, err := MarkdownToADF(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	if len(content) != 1 {
		t.Fatalf("content length = %d, want 1 (single paragraph)", len(content))
	}
	assertNodeType(t, content[0], "paragraph")

	text := extractTextFromNode(content[0])
	if !strings.Contains(text, "Line one") || !strings.Contains(text, "Line two") {
		t.Errorf("soft line break text = %q, want both lines present", text)
	}
}

func TestMarkdownToADF_AutoLink(t *testing.T) {
	md := "Visit <https://example.com> for details."
	adf, err := MarkdownToADF(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := docContent(t, adf)
	para := content[0].(map[string]any)
	inlines := para["content"].([]any)

	linkFound := false
	for _, inline := range inlines {
		node := inline.(map[string]any)
		if node["text"] == "https://example.com" {
			linkFound = true
			marks := node["marks"].([]any)
			for _, m := range marks {
				mark := m.(map[string]any)
				if mark["type"] == "link" {
					attrs := mark["attrs"].(map[string]any)
					if attrs["href"] != "https://example.com" {
						t.Errorf("autolink href = %q, want %q", attrs["href"], "https://example.com")
					}
				}
			}
		}
	}
	if !linkFound {
		t.Error("did not find autolink text node")
	}
}

func TestMarkdownToADF_ADFTextRoundtrip(t *testing.T) {
	md := "# Assessment\n\nSome analysis with **bold** and `code`."

	adf, err := MarkdownToADF(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	adfJSON, err := json.Marshal(adf)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var extracted ADFText
	if err := json.Unmarshal(adfJSON, &extracted); err != nil {
		t.Fatalf("ADFText unmarshal failed: %v", err)
	}

	plainText := string(extracted)
	if !strings.Contains(plainText, "Assessment") {
		t.Errorf("ADFText extraction missing heading text: %q", plainText)
	}
	if !strings.Contains(plainText, "bold") {
		t.Errorf("ADFText extraction missing bold text: %q", plainText)
	}
	if !strings.Contains(plainText, "code") {
		t.Errorf("ADFText extraction missing code text: %q", plainText)
	}
}

// --- test helpers ---

func assertDocType(t *testing.T, adf map[string]any) {
	t.Helper()
	if adf["type"] != "doc" {
		t.Errorf("type = %q, want %q", adf["type"], "doc")
	}
	if v, ok := adf["version"].(int); !ok || v != 1 {
		t.Errorf("version = %v, want 1", adf["version"])
	}
}

func docContent(t *testing.T, adf map[string]any) []any {
	t.Helper()
	assertDocType(t, adf)
	content, ok := adf["content"].([]any)
	if !ok {
		t.Fatal("content is not []any")
	}
	return content
}

func assertNodeType(t *testing.T, node any, want string) {
	t.Helper()
	m, ok := node.(map[string]any)
	if !ok {
		t.Fatalf("node is %T, not map[string]any", node)
	}
	if m["type"] != want {
		t.Errorf("node type = %q, want %q", m["type"], want)
	}
}

func assertHasMark(t *testing.T, node map[string]any, markType string) {
	t.Helper()
	marks, ok := node["marks"].([]any)
	if !ok {
		t.Fatalf("node has no marks, expected %q", markType)
	}
	for _, m := range marks {
		mark := m.(map[string]any)
		if mark["type"] == markType {
			return
		}
	}
	t.Errorf("mark %q not found in node marks", markType)
}

func extractTextFromNode(node any) string {
	m, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	if t, hasText := m["text"].(string); hasText {
		return t
	}
	content, ok := m["content"].([]any)
	if !ok {
		return ""
	}
	var buf strings.Builder
	for _, child := range content {
		buf.WriteString(extractTextFromNode(child))
	}
	return buf.String()
}

func extractAllText(adf map[string]any) string {
	return extractTextFromNode(adf)
}
