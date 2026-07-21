package triage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"triage-bot/config"
	"triage-bot/jira"
)

const (
	// assessmentVersion is bumped when a bot or workflow change warrants
	// re-assessing all issues. Existing comments with an older (or missing)
	// version trigger ActionUpdate on the next scan cycle.
	assessmentVersion = "3"

	markerPrefix  = "triage-bot | "
	versionPrefix = "v:"
	hashPrefix    = "desc:"
	hashLen       = 12
)

// JiraClient is the subset of the Jira client used by the processor.
type JiraClient interface {
	GetComments(ctx context.Context, key string) ([]jira.JiraComment, error)
	AddComment(ctx context.Context, key, comment string) error
	UpdateComment(ctx context.Context, key, commentID, body string) error
	AddCommentADF(ctx context.Context, key string, adfBody map[string]any) error
	UpdateCommentADF(ctx context.Context, key, commentID string, adfBody map[string]any) error
	AddLabel(ctx context.Context, key, label string) error
	RemoveLabel(ctx context.Context, key, label string) error
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
func (p *Processor) Process(ctx context.Context, issue jira.JiraIssue) error {
	key := issue.Key
	projectKey := issue.Fields.Project.Key
	description := string(issue.Fields.Description)
	descHash := computeHash(description)

	action, existingCommentID := p.determineAction(ctx, key, descHash)

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

	assessment, meta, err := p.executor.Run(ctx, key, projectKey)
	if err != nil {
		return fmt.Errorf("triage failed for %s: %w", key, err)
	}

	if err := p.postComment(ctx, key, action, existingCommentID, assessment, descHash); err != nil {
		return err
	}

	p.syncLabel(ctx, key, issue.Fields.Labels, meta)
	return nil
}

func (p *Processor) postComment(ctx context.Context, key string, action Action, existingCommentID, assessment, descHash string) error {
	adfBody, adfErr := buildADFComment(assessment, descHash)
	if adfErr != nil {
		p.logger.Warn("Failed to convert assessment to ADF, falling back to plain text",
			zap.String("issue", key),
			zap.Error(adfErr))
	}

	if p.cfg.DryRun {
		format := "adf"
		if adfErr != nil {
			format = "plain text"
		}
		p.logger.Info("DRY RUN: would post triage comment",
			zap.String("issue", key),
			zap.String("action", action.String()),
			zap.String("format", format))
		return nil
	}

	switch action {
	case ActionCreate:
		if adfErr == nil {
			if err := p.jira.AddCommentADF(ctx, key, adfBody); err != nil {
				return fmt.Errorf("failed to post comment on %s: %w", key, err)
			}
		} else {
			if err := p.jira.AddComment(ctx, key, appendHashFooter(assessment, descHash)); err != nil {
				return fmt.Errorf("failed to post comment on %s: %w", key, err)
			}
		}
		p.logger.Info("Posted triage comment", zap.String("issue", key))

	case ActionUpdate:
		if adfErr == nil {
			if err := p.jira.UpdateCommentADF(ctx, key, existingCommentID, adfBody); err != nil {
				return fmt.Errorf("failed to update comment on %s: %w", key, err)
			}
		} else {
			if err := p.jira.UpdateComment(ctx, key, existingCommentID, appendHashFooter(assessment, descHash)); err != nil {
				return fmt.Errorf("failed to update comment on %s: %w", key, err)
			}
		}
		p.logger.Info("Updated triage comment", zap.String("issue", key))
	}

	return nil
}

func (p *Processor) determineAction(ctx context.Context, key, descHash string) (Action, string) {
	comments, err := p.jira.GetComments(ctx, key)
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

	body := string(botComment.Body)
	storedHash := extractHash(body)
	storedVersion := extractVersion(body)

	if storedHash == descHash && storedVersion == assessmentVersion {
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
		hasMarker := strings.Contains(string(c.Body), markerPrefix)

		if isBot && hasMarker {
			return c
		}
	}
	return nil
}

func (p *Processor) syncLabel(ctx context.Context, key string, currentLabels []string, meta *Metadata) {
	outcome := meta.TriageOutcome(p.cfg.Triage.AutoFixThreshold)
	if outcome == "" {
		return
	}

	labelForOutcome := map[string]string{
		"autofix":      p.cfg.Triage.AutoFixLabel,
		"missing_info": p.cfg.Triage.MissingInfoLabel,
		"not_fixable":  p.cfg.Triage.NotFixableLabel,
	}

	targetLabel := labelForOutcome[outcome]

	// All managed triage labels — we remove stale ones when outcome changes.
	allLabels := []string{
		p.cfg.Triage.AutoFixLabel,
		p.cfg.Triage.MissingInfoLabel,
		p.cfg.Triage.NotFixableLabel,
	}

	if p.cfg.DryRun {
		if targetLabel != "" && !containsLabel(currentLabels, targetLabel) {
			p.logger.Info("DRY RUN: would add triage label",
				zap.String("issue", key),
				zap.String("label", targetLabel),
				zap.String("outcome", outcome))
		}
		for _, old := range allLabels {
			if old != "" && old != targetLabel && containsLabel(currentLabels, old) {
				p.logger.Info("DRY RUN: would remove stale triage label",
					zap.String("issue", key),
					zap.String("label", old))
			}
		}
		return
	}

	// Add the target label if not present and not disabled.
	if targetLabel != "" && !containsLabel(currentLabels, targetLabel) {
		if err := p.jira.AddLabel(ctx, key, targetLabel); err != nil {
			p.logger.Warn("Failed to add triage label",
				zap.String("issue", key),
				zap.String("label", targetLabel),
				zap.Error(err))
			return
		}
		likelihood := 0
		if meta != nil && meta.AutoFixLikelihood != nil {
			likelihood = *meta.AutoFixLikelihood
		}
		p.logger.Info("Applied triage label",
			zap.String("issue", key),
			zap.String("label", targetLabel),
			zap.String("outcome", outcome),
			zap.Int("likelihood", likelihood))
	}

	// Remove stale labels from previous triage runs.
	for _, old := range allLabels {
		if old != "" && old != targetLabel && containsLabel(currentLabels, old) {
			if err := p.jira.RemoveLabel(ctx, key, old); err != nil {
				p.logger.Warn("Failed to remove stale triage label",
					zap.String("issue", key),
					zap.String("label", old),
					zap.Error(err))
				continue
			}
			p.logger.Info("Removed stale triage label",
				zap.String("issue", key),
				zap.String("label", old))
		}
	}
}

func containsLabel(labels []string, target string) bool {
	for _, l := range labels {
		if l == target {
			return true
		}
	}
	return false
}

// trimInvisible strips BOM (U+FEFF) and other zero-width / invisible
// characters that LLM tool chains may emit at the boundaries of output.
func trimInvisible(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		if r <= ' ' {
			return true // ASCII control chars + space (superset of TrimSpace)
		}
		switch r {
		case '\u00A0', // no-break space
			'\uFEFF', // BOM / zero-width no-break space
			'\u200B', // zero-width space
			'\u200C', // zero-width non-joiner
			'\u200D', // zero-width joiner
			'\u2060', // word joiner
			'\uFFFE': // byte-order mark (swapped)
			return true
		}
		return false
	})
}

// buildADFComment converts the AI's markdown assessment to an ADF
// document and appends the description hash footer as ADF nodes.
// Returns an error for empty input so the caller can fall back to
// plain text.
func buildADFComment(assessment, hash string) (map[string]any, error) {
	cleaned := trimInvisible(assessment)

	adf, err := jira.MarkdownToADF(cleaned)
	if err != nil {
		return nil, fmt.Errorf("markdown to ADF: %w", err)
	}

	content, ok := adf["content"].([]any)
	if !ok {
		return nil, fmt.Errorf("ADF missing content array")
	}

	footerText := markerPrefix + versionPrefix + assessmentVersion + " | " + hashPrefix + hash

	content = append(content,
		map[string]any{"type": "rule"},
		map[string]any{
			"type": "paragraph",
			"content": []any{
				map[string]any{
					"type": "text",
					"text": footerText,
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
	return body + "\n\n---\n_" + markerPrefix + versionPrefix + assessmentVersion + " | " + hashPrefix + hash + "_\n"
}

// extractHash finds the last "desc:" marker and returns the hash.
// Works for both legacy ("triage-bot | desc:HASH") and versioned
// ("triage-bot | v:N | desc:HASH") formats because "desc:" appears
// in both and LastIndex finds the correct position regardless.
func extractHash(body string) string {
	idx := strings.LastIndex(body, hashPrefix)
	if idx < 0 {
		return ""
	}

	rest := body[idx+len(hashPrefix):]
	for i, c := range rest {
		if c == '_' || c == '\n' {
			rest = rest[:i]
			break
		}
	}
	if len(rest) != hashLen {
		return ""
	}
	return rest
}

func extractVersion(body string) string {
	idx := strings.LastIndex(body, markerPrefix+versionPrefix)
	if idx < 0 {
		return ""
	}
	start := idx + len(markerPrefix+versionPrefix)
	rest := body[start:]

	end := strings.IndexAny(rest, " |_\n")
	if end < 0 {
		return ""
	}
	return rest[:end]
}
