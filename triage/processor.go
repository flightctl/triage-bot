package triage

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"triage-bot/config"
	"triage-bot/jira"
)

const (
	hashPrefix = "triage-bot | desc:"
	hashLen    = 12
)

// JiraClient is the subset of the Jira client used by the processor.
type JiraClient interface {
	GetComments(key string) ([]jira.JiraComment, error)
	AddComment(key, comment string) error
	UpdateComment(key, commentID, body string) error
	AddCommentADF(key string, adfBody map[string]any) error
	UpdateCommentADF(key, commentID string, adfBody map[string]any) error
	AddLabel(key, label string) error
}

// Action describes what the processor decided to do for an issue.
type Action int

const (
	ActionSkip   Action = iota // triage is current
	ActionCreate               // no bot comment, run triage and post
	ActionUpdate               // description changed, re-run and replace
)

func (a Action) String() string {
	switch a {
	case ActionSkip:
		return "skip"
	case ActionCreate:
		return "create"
	case ActionUpdate:
		return "update"
	default:
		return "unknown"
	}
}

// Processor handles the per-issue triage logic: checking for existing
// bot comments, running the assessment, and posting/updating comments.
type Processor struct {
	jira     JiraClient
	executor *Executor
	cfg      config.Config
	logger   *zap.Logger
}

func NewProcessor(jiraClient JiraClient, executor *Executor, cfg config.Config, logger *zap.Logger) *Processor {
	return &Processor{
		jira:     jiraClient,
		executor: executor,
		cfg:      cfg,
		logger:   logger,
	}
}

// Process runs the triage workflow for a single issue.
func (p *Processor) Process(issue jira.JiraIssue) error {
	key := issue.Key
	projectKey := issue.Fields.Project.Key
	description := string(issue.Fields.Description)
	descHash := computeHash(description)

	action, existingCommentID := p.determineAction(key, descHash)

	switch action {
	case ActionSkip:
		p.logger.Debug("Triage is current, skipping",
			zap.String("issue", key))
		return nil

	case ActionCreate:
		p.logger.Info("Running triage assessment (new)",
			zap.String("issue", key))

	case ActionUpdate:
		p.logger.Info("Running triage assessment (description changed)",
			zap.String("issue", key))
	}

	assessment, meta, err := p.executor.Run(key, projectKey)
	if err != nil {
		return fmt.Errorf("triage failed for %s: %w", key, err)
	}

	adfBody, err := buildADFComment(assessment, descHash)
	if err != nil {
		p.logger.Warn("Failed to parse ADF output, falling back to plain text",
			zap.String("issue", key),
			zap.Error(err))
		return p.postPlainText(key, action, existingCommentID, assessment, descHash, meta)
	}

	if p.cfg.DryRun {
		p.logger.Info("DRY RUN: would post triage comment",
			zap.String("issue", key),
			zap.String("action", action.String()),
			zap.Any("adf", adfBody))
		if meta != nil {
			p.logger.Info("DRY RUN: metadata sidecar",
				zap.String("issue", key),
				zap.String("recommendation", meta.Recommendation),
				zap.String("confidence", meta.Confidence))
		}
		return nil
	}

	switch action {
	case ActionCreate:
		if err := p.jira.AddCommentADF(key, adfBody); err != nil {
			return fmt.Errorf("failed to post comment on %s: %w", key, err)
		}
		p.logger.Info("Posted triage comment", zap.String("issue", key))

	case ActionUpdate:
		if err := p.jira.UpdateCommentADF(key, existingCommentID, adfBody); err != nil {
			return fmt.Errorf("failed to update comment on %s: %w", key, err)
		}
		p.logger.Info("Updated triage comment", zap.String("issue", key))
	}

	p.applyLabel(key, meta)
	return nil
}

func (p *Processor) postPlainText(key string, action Action, commentID, assessment, descHash string, meta *Metadata) error {
	commentBody := appendHashFooter(assessment, descHash)

	if p.cfg.DryRun {
		p.logger.Info("DRY RUN: would post triage comment (plain text)",
			zap.String("issue", key),
			zap.String("action", action.String()),
			zap.String("comment", commentBody))
		return nil
	}

	switch action {
	case ActionCreate:
		if err := p.jira.AddComment(key, commentBody); err != nil {
			return fmt.Errorf("failed to post comment on %s: %w", key, err)
		}
	case ActionUpdate:
		if err := p.jira.UpdateComment(key, commentID, commentBody); err != nil {
			return fmt.Errorf("failed to update comment on %s: %w", key, err)
		}
	}

	p.logger.Info("Posted triage comment (plain text fallback)", zap.String("issue", key))
	p.applyLabel(key, meta)
	return nil
}

func (p *Processor) determineAction(key, descHash string) (Action, string) {
	comments, err := p.jira.GetComments(key)
	if err != nil {
		p.logger.Warn("Failed to fetch comments, will create new comment",
			zap.String("issue", key),
			zap.Error(err))
		return ActionCreate, ""
	}

	botComment := p.findBotComment(comments)
	if botComment == nil {
		return ActionCreate, ""
	}

	storedHash := extractHash(string(botComment.Body))
	if storedHash == descHash {
		return ActionSkip, botComment.ID
	}

	return ActionUpdate, botComment.ID
}

func (p *Processor) findBotComment(comments []jira.JiraComment) *jira.JiraComment {
	botUser := p.cfg.Jira.BotUsername
	for i := len(comments) - 1; i >= 0; i-- {
		c := &comments[i]
		isBot := c.Author.EmailAddress == botUser ||
			c.Author.DisplayName == botUser ||
			c.Author.Name == botUser
		hasMarker := strings.Contains(string(c.Body), hashPrefix)

		if isBot && hasMarker {
			return c
		}
	}
	return nil
}

func (p *Processor) applyLabel(key string, meta *Metadata) {
	if p.cfg.Triage.AutoFixLabel == "" {
		return
	}
	if !meta.ShouldApplyAutoFixLabel(p.cfg.Triage.AutoFixThreshold) {
		return
	}
	if err := p.jira.AddLabel(key, p.cfg.Triage.AutoFixLabel); err != nil {
		p.logger.Warn("Failed to add auto-fix label",
			zap.String("issue", key),
			zap.String("label", p.cfg.Triage.AutoFixLabel),
			zap.Error(err))
		return
	}
	p.logger.Info("Applied auto-fix label",
		zap.String("issue", key),
		zap.String("label", p.cfg.Triage.AutoFixLabel),
		zap.Int("likelihood", *meta.AutoFixLikelihood))
}

// buildADFComment parses the AI's ADF JSON output and appends the
// description hash footer as ADF nodes. If parsing fails, returns an
// error so the caller can fall back to plain text.
func buildADFComment(assessment, hash string) (map[string]any, error) {
	var adf map[string]any
	if err := json.Unmarshal([]byte(assessment), &adf); err != nil {
		return nil, fmt.Errorf("invalid ADF JSON: %w", err)
	}

	if adf["type"] != "doc" {
		return nil, fmt.Errorf("ADF missing top-level type:doc")
	}

	content, ok := adf["content"].([]any)
	if !ok {
		return nil, fmt.Errorf("ADF missing content array")
	}

	// Append: horizontal rule + hash footer paragraph
	content = append(content,
		map[string]any{"type": "rule"},
		map[string]any{
			"type": "paragraph",
			"content": []any{
				map[string]any{
					"type": "text",
					"text": hashPrefix + hash,
					"marks": []any{
						map[string]any{"type": "em"},
					},
				},
			},
		},
	)

	adf["content"] = content
	return adf, nil
}

// computeHash returns the first hashLen hex chars of the SHA-256 of s.
func computeHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:])[:hashLen]
}

func appendHashFooter(body, hash string) string {
	body = strings.TrimRight(body, "\n")
	return body + "\n\n---\n_" + hashPrefix + hash + "_\n"
}

func extractHash(body string) string {
	idx := strings.LastIndex(body, hashPrefix)
	if idx < 0 {
		return ""
	}
	start := idx + len(hashPrefix)
	rest := body[start:]
	for i, c := range rest {
		if c == '_' || c == '\n' {
			return rest[:i]
		}
	}
	return rest
}
