package scanner

import (
	"context"
	"fmt"
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
	s.scanInactive(ctx)

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
			s.scanInactive(ctx)
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

func (s *Scanner) scanInactive(ctx context.Context) {
	staleLabel := s.cfg.Triage.StaleLabel
	if staleLabel == "" {
		return
	}

	if s.cfg.Triage.StaleDays <= 0 {
		return
	}

	jql := s.buildInactiveJQL()
	s.logger.Info("Scanning for inactive open bugs", zap.String("jql", jql))

	allIssues, err := s.searchAll(ctx, jql)
	if err != nil {
		s.logger.Error("Failed to search for inactive bugs", zap.Error(err))
		return
	}
	if len(allIssues) == 0 {
		return
	}

	s.logger.Info("Found inactive open bugs", zap.Int("count", len(allIssues)))
	for _, issue := range allIssues {
		if ctx.Err() != nil {
			break
		}
		if s.cfg.DryRun {
			s.logger.Info("DRY RUN: would add stale label to inactive bug",
				zap.String("issue", issue.Key), zap.String("label", staleLabel))
			continue
		}
		if err := s.jiraClient.AddLabel(ctx, issue.Key, staleLabel); err != nil {
			s.logger.Error("Failed to add stale label to inactive bug",
				zap.String("issue", issue.Key), zap.Error(err))
			continue
		}
		s.logger.Info("Marked inactive bug as stale", zap.String("issue", issue.Key))
	}
}

func (s *Scanner) buildInactiveJQL() string {
	projects := make([]string, len(s.cfg.Jira.ProjectKeys))
	for i, k := range s.cfg.Jira.ProjectKeys {
		projects[i] = fmt.Sprintf("%q", k)
	}

	jql := fmt.Sprintf(
		`project IN (%s) AND issuetype = Bug AND status = %q AND updated <= "-%dd"`,
		strings.Join(projects, ", "),
		s.cfg.Triage.StaleStatus,
		s.cfg.Triage.StaleDays,
	)

	if len(s.cfg.Jira.ExcludedComponents) > 0 {
		comps := make([]string, len(s.cfg.Jira.ExcludedComponents))
		for i, c := range s.cfg.Jira.ExcludedComponents {
			comps[i] = fmt.Sprintf("%q", c)
		}
		jql += fmt.Sprintf(" AND (component is EMPTY OR component NOT IN (%s))", strings.Join(comps, ", "))
	}

	jql += " ORDER BY key ASC"
	return jql
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
		jql += fmt.Sprintf(" AND (component is EMPTY OR component NOT IN (%s))", strings.Join(comps, ", "))
	}

	jql += " ORDER BY key ASC"
	return jql
}

// Verify Processor implements IssueProcessor at compile time.
var _ IssueProcessor = (*triage.Processor)(nil)
