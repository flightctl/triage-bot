package jira

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// MarkdownToADF converts a markdown string to an Atlassian Document Format
// document suitable for posting to Jira Cloud API v3.
func MarkdownToADF(markdown string) (map[string]any, error) {
	trimmed := strings.TrimSpace(markdown)
	if trimmed == "" {
		return nil, fmt.Errorf("empty markdown input")
	}

	src := []byte(trimmed)

	md := goldmark.New(
		goldmark.WithExtensions(extension.Table),
	)

	doc := md.Parser().Parse(text.NewReader(src))

	w := &adfWalker{
		src:   src,
		stack: [][]any{{}},
	}

	if err := ast.Walk(doc, w.walk); err != nil {
		return nil, fmt.Errorf("markdown conversion failed: %w", err)
	}

	return map[string]any{
		"type":    "doc",
		"version": 1,
		"content": w.stack[0],
	}, nil
}

type adfWalker struct {
	src   []byte
	stack [][]any
	marks []map[string]any
}

func (w *adfWalker) push() {
	w.stack = append(w.stack, []any{})
}

func (w *adfWalker) pop() []any {
	n := len(w.stack) - 1
	top := w.stack[n]
	w.stack = w.stack[:n]
	return top
}

func (w *adfWalker) append(node map[string]any) {
	top := len(w.stack) - 1
	w.stack[top] = append(w.stack[top], node)
}

func (w *adfWalker) pushMark(m map[string]any) {
	w.marks = append(w.marks, m)
}

func (w *adfWalker) popMark() {
	w.marks = w.marks[:len(w.marks)-1]
}

func (w *adfWalker) currentMarks() []any {
	if len(w.marks) == 0 {
		return nil
	}
	out := make([]any, len(w.marks))
	for i, m := range w.marks {
		out[i] = deepCopyMap(m)
	}
	return out
}

func deepCopyMap(m map[string]any) map[string]any {
	cp := make(map[string]any, len(m))
	for k, v := range m {
		if nested, ok := v.(map[string]any); ok {
			cp[k] = deepCopyMap(nested)
		} else {
			cp[k] = v
		}
	}
	return cp
}

func (w *adfWalker) walk(n ast.Node, entering bool) (ast.WalkStatus, error) {
	switch node := n.(type) {
	case *ast.Document:
		return ast.WalkContinue, nil

	case *ast.Heading:
		return w.handleBlock(entering, "heading", map[string]any{"level": node.Level})

	case *ast.Paragraph:
		return w.handleParagraph(node, entering)

	case *ast.TextBlock:
		return w.handleBlock(entering, "paragraph", nil)

	case *ast.ThematicBreak:
		if entering {
			w.append(map[string]any{"type": "rule"})
		}
		return ast.WalkContinue, nil

	case *ast.Blockquote:
		return w.handleBlock(entering, "blockquote", nil)

	case *ast.FencedCodeBlock:
		return w.handleCodeBlock(node, entering)

	case *ast.CodeBlock:
		return w.handleIndentedCodeBlock(node, entering)

	case *ast.List:
		return w.handleList(node, entering)

	case *ast.ListItem:
		return w.handleBlock(entering, "listItem", nil)

	case *ast.Text:
		return w.handleText(node, entering)

	case *ast.String:
		if entering {
			w.appendTextNode(string(node.Value))
		}
		return ast.WalkContinue, nil

	case *ast.CodeSpan:
		return w.handleCodeSpan(node, entering)

	case *ast.Emphasis:
		return w.handleEmphasis(node, entering)

	case *ast.Link:
		return w.handleLink(node, entering)

	case *ast.AutoLink:
		return w.handleAutoLink(node, entering)

	case *east.Table:
		return w.handleBlock(entering, "table", nil)

	case *east.TableHeader:
		return w.handleBlock(entering, "tableRow", nil)

	case *east.TableRow:
		return w.handleBlock(entering, "tableRow", nil)

	case *east.TableCell:
		return w.handleTableCell(node, entering)

	default:
		return ast.WalkContinue, nil
	}
}

func (w *adfWalker) handleBlock(entering bool, nodeType string, attrs map[string]any) (ast.WalkStatus, error) {
	if entering {
		w.push()
	} else {
		content := w.pop()
		node := map[string]any{
			"type":    nodeType,
			"content": content,
		}
		if attrs != nil {
			node["attrs"] = attrs
		}
		w.append(node)
	}
	return ast.WalkContinue, nil
}

func (w *adfWalker) handleParagraph(node *ast.Paragraph, entering bool) (ast.WalkStatus, error) {
	// Goldmark wraps table cell content in paragraphs, but ADF table
	// cells already contain their own paragraph. When a paragraph is a
	// direct child of a table cell, emit its inline content directly
	// without wrapping in another paragraph node.
	if node.Parent() != nil {
		if _, ok := node.Parent().(*east.TableCell); ok {
			return ast.WalkContinue, nil
		}
	}
	return w.handleBlock(entering, "paragraph", nil)
}

func (w *adfWalker) handleCodeBlock(node *ast.FencedCodeBlock, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	w.emitCodeBlock(node, string(node.Language(w.src)))
	return ast.WalkSkipChildren, nil
}

func (w *adfWalker) handleIndentedCodeBlock(node *ast.CodeBlock, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	w.emitCodeBlock(node, "")
	return ast.WalkSkipChildren, nil
}

func (w *adfWalker) emitCodeBlock(node ast.Node, language string) {
	var buf strings.Builder
	for i := 0; i < node.Lines().Len(); i++ {
		line := node.Lines().At(i)
		buf.Write(line.Value(w.src))
	}
	codeText := strings.TrimRight(buf.String(), "\n")

	adfNode := map[string]any{
		"type": "codeBlock",
		"content": []any{
			map[string]any{"type": "text", "text": codeText},
		},
	}
	if language != "" {
		adfNode["attrs"] = map[string]any{"language": language}
	}
	w.append(adfNode)
}

func (w *adfWalker) handleList(node *ast.List, entering bool) (ast.WalkStatus, error) {
	nodeType := "bulletList"
	if node.IsOrdered() {
		nodeType = "orderedList"
	}
	return w.handleBlock(entering, nodeType, nil)
}

func (w *adfWalker) handleText(node *ast.Text, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	w.appendTextNode(string(node.Segment.Value(w.src)))
	if node.HardLineBreak() {
		w.append(map[string]any{"type": "hardBreak"})
	} else if node.SoftLineBreak() {
		w.appendTextNode(" ")
	}
	return ast.WalkContinue, nil
}

func (w *adfWalker) handleCodeSpan(node *ast.CodeSpan, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}

	var buf strings.Builder
	for c := node.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			buf.Write(t.Segment.Value(w.src))
		}
	}

	textNode := map[string]any{
		"type": "text",
		"text": buf.String(),
		"marks": []any{
			map[string]any{"type": "code"},
		},
	}
	w.append(textNode)
	return ast.WalkSkipChildren, nil
}

func (w *adfWalker) handleEmphasis(node *ast.Emphasis, entering bool) (ast.WalkStatus, error) {
	markType := "em"
	if node.Level == 2 {
		markType = "strong"
	}
	if entering {
		w.pushMark(map[string]any{"type": markType})
	} else {
		w.popMark()
	}
	return ast.WalkContinue, nil
}

func (w *adfWalker) handleLink(node *ast.Link, entering bool) (ast.WalkStatus, error) {
	if entering {
		w.pushMark(map[string]any{
			"type":  "link",
			"attrs": map[string]any{"href": string(node.Destination)},
		})
	} else {
		w.popMark()
	}
	return ast.WalkContinue, nil
}

func (w *adfWalker) handleAutoLink(node *ast.AutoLink, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	url := string(node.URL(w.src))
	textNode := map[string]any{
		"type": "text",
		"text": url,
		"marks": []any{
			map[string]any{
				"type":  "link",
				"attrs": map[string]any{"href": url},
			},
		},
	}
	w.append(textNode)
	return ast.WalkSkipChildren, nil
}

func (w *adfWalker) handleTableCell(node *east.TableCell, entering bool) (ast.WalkStatus, error) {
	cellType := "tableCell"
	if node.Parent() != nil {
		if _, ok := node.Parent().(*east.TableHeader); ok {
			cellType = "tableHeader"
		}
	}

	if entering {
		w.push()
		// Push an inner content slice for the cell's paragraph content.
		w.push()
	} else {
		// Pop the inner paragraph content and wrap it.
		innerContent := w.pop()
		para := map[string]any{
			"type":    "paragraph",
			"content": innerContent,
		}
		w.append(para)
		cellContent := w.pop()
		w.append(map[string]any{
			"type":    cellType,
			"content": cellContent,
		})
	}
	return ast.WalkContinue, nil
}

func (w *adfWalker) appendTextNode(text string) {
	if text == "" {
		return
	}
	node := map[string]any{
		"type": "text",
		"text": text,
	}
	if marks := w.currentMarks(); marks != nil {
		node["marks"] = marks
	}
	w.append(node)
}
