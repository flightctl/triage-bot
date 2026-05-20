package jira

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// JiraTime handles Jira's custom date format.
type JiraTime struct {
	time.Time
}

func (jt *JiraTime) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		jt.Time = time.Time{}
		return nil
	}
	s := string(b)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if s == "null" || s == "" {
		jt.Time = time.Time{}
		return nil
	}
	t, err := time.Parse("2006-01-02T15:04:05.000-0700", s)
	if err == nil {
		jt.Time = t
		return nil
	}
	t, err = time.Parse("2006-01-02T15:04:05.000Z", s)
	if err == nil {
		jt.Time = t
		return nil
	}
	return fmt.Errorf("could not parse JiraTime: %w", err)
}

type JiraIssue struct {
	ID     string     `json:"id"`
	Self   string     `json:"self"`
	Key    string     `json:"key"`
	Fields JiraFields `json:"fields"`
}

type JiraFields struct {
	Summary     string          `json:"summary"`
	Description ADFText         `json:"description"`
	Status      JiraStatus      `json:"status"`
	IssueType   JiraIssueType   `json:"issuetype"`
	Project     JiraProject     `json:"project"`
	Components  []JiraComponent `json:"components"`
	Labels      []string        `json:"labels"`
	Created     JiraTime        `json:"created"`
	Updated     JiraTime        `json:"updated"`
	Creator     JiraUser        `json:"creator"`
	Reporter    JiraUser        `json:"reporter"`
	Assignee    *JiraUser       `json:"assignee,omitempty"`
	Comment     JiraComments    `json:"comment,omitempty"`
}

type JiraStatus struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type JiraIssueType struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type JiraProject struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

type JiraUser struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
}

type JiraComponent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type JiraComments struct {
	Comments   []JiraComment `json:"comments"`
	MaxResults int           `json:"maxResults"`
	Total      int           `json:"total"`
	StartAt    int           `json:"startAt"`
}

type JiraComment struct {
	ID      string   `json:"id"`
	Body    ADFText  `json:"body"`
	Author  JiraUser `json:"author"`
	Created JiraTime `json:"created"`
	Updated JiraTime `json:"updated"`
}

type JiraSearchResponse struct {
	Issues        []JiraIssue `json:"issues"`
	NextPageToken string      `json:"nextPageToken,omitempty"`
	IsLast        bool        `json:"isLast"`
}

// ADFText transparently unmarshals from Jira Cloud's Atlassian Document
// Format. When the JSON value is a string, it stores directly. When an
// ADF object, it extracts plain text.
type ADFText string

func (a *ADFText) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*a = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*a = ADFText(s)
		return nil
	}
	var doc adfNode
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("unmarshal ADF: %w", err)
	}
	*a = ADFText(extractADFText(&doc))
	return nil
}

type adfNode struct {
	Type    string    `json:"type"`
	Text    string    `json:"text,omitempty"`
	Content []adfNode `json:"content,omitempty"`
}

func extractADFText(node *adfNode) string {
	if node == nil {
		return ""
	}
	if node.Type == "text" {
		return node.Text
	}
	if node.Type == "hardBreak" {
		return "\n"
	}
	var b strings.Builder
	for i, child := range node.Content {
		b.WriteString(extractADFText(&child))
		if isBlockNode(child.Type) && i < len(node.Content)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func isBlockNode(nodeType string) bool {
	switch nodeType {
	case "paragraph", "heading", "blockquote", "codeBlock",
		"orderedList", "bulletList", "listItem", "rule",
		"table", "tableRow", "tableCell", "tableHeader",
		"mediaSingle", "mediaGroup", "panel", "expand",
		"extension", "decisionList", "taskList":
		return true
	}
	return false
}

// TextToADF converts plain text to a minimal Atlassian Document Format
// object for Jira Cloud API v3 write operations.
func TextToADF(text string) map[string]any {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	paragraphs := strings.Split(text, "\n")
	content := make([]map[string]any, 0, len(paragraphs))
	for _, line := range paragraphs {
		if line == "" {
			content = append(content, map[string]any{
				"type":    "paragraph",
				"content": []map[string]any{},
			})
			continue
		}
		content = append(content, map[string]any{
			"type": "paragraph",
			"content": []map[string]any{
				{"type": "text", "text": line},
			},
		})
	}
	return map[string]any{
		"type":    "doc",
		"version": 1,
		"content": content,
	}
}
