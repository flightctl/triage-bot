package scanner

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"triage-bot/config"
	"triage-bot/jira"
	"triage-bot/triage"
)

// ScannerJiraClient is the subset of jira.Client used by the scanner.
type ScannerJiraClient interface {
	SearchTickets(ctx context.Context, jql string, maxResults int, nextPageToken string) (*jira.JiraSearchResponse, error)
	AddLabel(ctx context.Context, key, label string) error
	RemoveLabel(ctx context.Context, key, label string) error
}

// IssueProcessor processes a single Jira issue.
type IssueProcessor interface {
	Process(ctx context.Context, issue jira.JiraIssue) error
}

// Scanner polls Jira for bugs and dispatches them to the processor.
type Scanner struct {
	jiraClient ScannerJiraClient
	processor  IssueProcessor
	cfg        config.Config
	logger     *zap.Logger

	inFlight *InFlight

	cancel context.CancelFunc
	done   chan struct{}
}

func NewScanner(jiraClient ScannerJiraClient, processor IssueProcessor, inFlight *InFlight, cfg config.Config, logger *zap.Logger) *Scanner {
	return &Scanner{
		jiraClient: jiraClient,
		processor:  processor,
		cfg:        cfg,
		logger:     logger,
		inFlight:   inFlight,
	}
}

func (s *Scanner) Start(ctx context.Context) {
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})
	go s.run(ctx)
}

func (s *Scanner) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
}

func (s *Scanner) run(ctx context.Context) {
	defer close(s.done)

	s.scan(ctx)
	s.scanStale(ctx)
	s.scanUntriaged(ctx)

	interval := time.Duration(s.cfg.Jira.IntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("Scanner shutting down")
			return
		case <-ticker.C:
			s.scan(ctx)
			s.scanStale(ctx)
			s.scanUntriaged(ctx)
		}
	}
}

func (s *Scanner) searchAll(ctx context.Context, jql string) ([]jira.JiraIssue, error) {
	var issues []jira.JiraIssue
	nextPageToken := ""
	for {
		result, err := s.jiraClient.SearchTickets(ctx, jql, s.cfg.Jira.MaxResults, nextPageToken)
		if err != nil {
			return nil, err
		}
		issues = append(issues, result.Issues...)
		if result.IsLast || result.NextPageToken == "" {
			break
		}
		nextPageToken = result.NextPageToken
	}
	return issues, nil
}

func (s *Scanner) scan(ctx context.Context) {
	jql := s.buildJQL()
	s.logger.Info("Scanning for bugs", zap.String("jql", jql))

	allIssues, err := s.searchAll(ctx, jql)
	if err != nil {
		s.logger.Error("Failed to search Jira", zap.Error(err))
		return
	}

	s.logger.Info("Found issues", zap.Int("count", len(allIssues)))

	if len(allIssues) == 0 {
		return
	}

	sem := make(chan struct{}, s.cfg.AI.MaxConcurrent)
	var wg sync.WaitGroup

	for _, issue := range allIssues {
		if ctx.Err() != nil {
			break
		}

		if !s.inFlight.TryAcquire(issue.Key) {
			s.logger.Debug("Issue already in-flight, skipping",
				zap.String("issue", issue.Key))
			continue
		}

		sem <- struct{}{}
		wg.Add(1)
		go func(iss jira.JiraIssue) {
			defer wg.Done()
			defer func() { <-sem }()
			defer s.inFlight.Release(iss.Key)

			if err := s.processor.Process(ctx, iss); err != nil {
				s.logger.Error("Failed to process issue",
					zap.String("issue", iss.Key),
					zap.Error(err))
			}
		}(issue)
	}

	wg.Wait()
}

func (s *Scanner) scanStale(ctx context.Context) {
	staleLabel := s.cfg.Triage.StaleLabel
	if staleLabel == "" {
		return
	}

	autofixLabel := s.cfg.Triage.AutoFixLabel
	if autofixLabel == "" {
		return
	}

	if len(s.cfg.Triage.ProgressionLabels) == 0 {
		s.logger.Warn("Stale label configured but progression_labels is empty, skipping stale scan")
		return
	}

	jql := s.buildStaleJQL()
	s.logger.Info("Scanning for stale issues", zap.String("jql", jql))

	allIssues, err := s.searchAll(ctx, jql)
	if err != nil {
		s.logger.Error("Failed to search Jira for stale issues", zap.Error(err))
		return
	}

	if len(allIssues) == 0 {
		return
	}

	s.logger.Info("Found stale issues", zap.Int("count", len(allIssues)))

	// Label mutations are lightweight Jira API calls (no AI invocation),
	// so we skip the concurrency semaphore used by scan().
	for _, issue := range allIssues {
		if ctx.Err() != nil {
			break
		}

		if s.cfg.DryRun {
			s.logger.Info("DRY RUN: would mark stale",
				zap.String("issue", issue.Key),
				zap.String("add", staleLabel),
				zap.String("remove", autofixLabel))
			continue
		}

		if err := s.jiraClient.AddLabel(ctx, issue.Key, staleLabel); err != nil {
			s.logger.Error("Failed to add stale label",
				zap.String("issue", issue.Key),
				zap.Error(err))
			continue
		}
		if err := s.jiraClient.RemoveLabel(ctx, issue.Key, autofixLabel); err != nil {
			s.logger.Error("Failed to remove autofix label from stale issue",
				zap.String("issue", issue.Key),
				zap.Error(err))
			continue
		}
		s.logger.Info("Marked issue as stale",
			zap.String("issue", issue.Key))
	}
}

func (s *Scanner) buildStaleJQL() string {
	projects := make([]string, len(s.cfg.Jira.ProjectKeys))
	for i, k := range s.cfg.Jira.ProjectKeys {
		projects[i] = fmt.Sprintf("%q", k)
	}

	autofixLabel := s.cfg.Triage.AutoFixLabel
	staleLabel := s.cfg.Triage.StaleLabel

	allExcluded := slices.Concat(s.cfg.Triage.ProgressionLabels, []string{staleLabel})
	quoted := make([]string, len(allExcluded))
	for i, l := range allExcluded {
		quoted[i] = fmt.Sprintf("%q", l)
	}

	return fmt.Sprintf(
		"project IN (%s) AND issuetype = Bug AND statusCategory = Done AND labels = %q AND labels NOT IN (%s) ORDER BY key ASC",
		strings.Join(projects, ", "),
		autofixLabel,
		strings.Join(quoted, ", "),
	)
}

func (s *Scanner) scanUntriaged(ctx context.Context) {
	staleLabel := s.cfg.Triage.StaleLabel
	if staleLabel == "" {
		return
	}

	var pipelineLabels []string
	for _, l := range []string{s.cfg.Triage.AutoFixLabel, s.cfg.Triage.MissingInfoLabel, s.cfg.Triage.NotFixableLabel, staleLabel} {
		if l != "" {
			pipelineLabels = append(pipelineLabels, l)
		}
	}
	pipelineLabels = append(pipelineLabels, s.cfg.Triage.ProgressionLabels...)

	jql := s.buildUntriagedJQL(pipelineLabels)
	s.logger.Info("Scanning for untriaged closed bugs", zap.String("jql", jql))

	allIssues, err := s.searchAll(ctx, jql)
	if err != nil {
		s.logger.Error("Failed to search for untriaged bugs", zap.Error(err))
		return
	}
	if len(allIssues) == 0 {
		return
	}

	s.logger.Info("Found untriaged closed bugs", zap.Int("count", len(allIssues)))
	for _, issue := range allIssues {
		if ctx.Err() != nil {
			break
		}
		if s.cfg.DryRun {
			s.logger.Info("DRY RUN: would add stale label to untriaged bug",
				zap.String("issue", issue.Key), zap.String("label", staleLabel))
			continue
		}
		if err := s.jiraClient.AddLabel(ctx, issue.Key, staleLabel); err != nil {
			s.logger.Error("Failed to add stale label to untriaged bug",
				zap.String("issue", issue.Key), zap.Error(err))
			continue
		}
		s.logger.Info("Marked untriaged bug as stale", zap.String("issue", issue.Key))
	}
}

func (s *Scanner) buildUntriagedJQL(pipelineLabels []string) string {
	projects := make([]string, len(s.cfg.Jira.ProjectKeys))
	for i, k := range s.cfg.Jira.ProjectKeys {
		projects[i] = fmt.Sprintf("%q", k)
	}
	quoted := make([]string, len(pipelineLabels))
	for i, l := range pipelineLabels {
		quoted[i] = fmt.Sprintf("%q", l)
	}
	return fmt.Sprintf(
		"project IN (%s) AND issuetype = Bug AND statusCategory = Done AND (labels is EMPTY OR labels NOT IN (%s)) ORDER BY key ASC",
		strings.Join(projects, ", "),
		strings.Join(quoted, ", "),
	)
}

func (s *Scanner) buildJQL() string {
	projects := make([]string, len(s.cfg.Jira.ProjectKeys))
	for i, k := range s.cfg.Jira.ProjectKeys {
		projects[i] = fmt.Sprintf("%q", k)
	}

	jql := fmt.Sprintf(
		"project IN (%s) AND issuetype = Bug AND statusCategory != Done",
		strings.Join(projects, ", "),
	)

	if len(s.cfg.Jira.ExcludedComponents) > 0 {
		comps := make([]string, len(s.cfg.Jira.ExcludedComponents))
		for i, c := range s.cfg.Jira.ExcludedComponents {
			comps[i] = fmt.Sprintf("%q", c)
		}
		jql += fmt.Sprintf(" AND component NOT IN (%s)", strings.Join(comps, ", "))
	}

	jql += " ORDER BY key ASC"
	return jql
}

// Verify Processor implements IssueProcessor at compile time.
var _ IssueProcessor = (*triage.Processor)(nil)
