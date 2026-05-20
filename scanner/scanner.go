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

// IssueProcessor processes a single Jira issue.
type IssueProcessor interface {
	Process(issue jira.JiraIssue) error
}

// Scanner polls Jira for bugs and dispatches them to the processor.
type Scanner struct {
	jiraClient *jira.Client
	processor  IssueProcessor
	cfg        config.Config
	logger     *zap.Logger

	inFlight   map[string]struct{}
	inFlightMu sync.Mutex

	cancel context.CancelFunc
	done   chan struct{}
}

func NewScanner(jiraClient *jira.Client, processor IssueProcessor, cfg config.Config, logger *zap.Logger) *Scanner {
	return &Scanner{
		jiraClient: jiraClient,
		processor:  processor,
		cfg:        cfg,
		logger:     logger,
		inFlight:   make(map[string]struct{}),
	}
}

func (s *Scanner) Start(ctx context.Context) {
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
		}
	}
}

func (s *Scanner) scan(ctx context.Context) {
	jql := s.buildJQL()
	s.logger.Info("Scanning for bugs", zap.String("jql", jql))

	result, err := s.jiraClient.SearchTickets(jql, s.cfg.Jira.MaxResults)
	if err != nil {
		s.logger.Error("Failed to search Jira", zap.Error(err))
		return
	}

	s.logger.Info("Found issues", zap.Int("count", len(result.Issues)))

	if len(result.Issues) == 0 {
		return
	}

	sem := make(chan struct{}, s.cfg.AI.MaxConcurrent)
	var wg sync.WaitGroup

	for _, issue := range result.Issues {
		if ctx.Err() != nil {
			break
		}

		if !s.tryAcquire(issue.Key) {
			s.logger.Debug("Issue already in-flight, skipping",
				zap.String("issue", issue.Key))
			continue
		}

		sem <- struct{}{}
		wg.Add(1)
		go func(iss jira.JiraIssue) {
			defer wg.Done()
			defer func() { <-sem }()
			defer s.release(iss.Key)

			if err := s.processor.Process(iss); err != nil {
				s.logger.Error("Failed to process issue",
					zap.String("issue", iss.Key),
					zap.Error(err))
			}
		}(issue)
	}

	wg.Wait()
}

func (s *Scanner) tryAcquire(key string) bool {
	s.inFlightMu.Lock()
	defer s.inFlightMu.Unlock()
	if _, exists := s.inFlight[key]; exists {
		return false
	}
	s.inFlight[key] = struct{}{}
	return true
}

func (s *Scanner) release(key string) {
	s.inFlightMu.Lock()
	defer s.inFlightMu.Unlock()
	delete(s.inFlight, key)
}

// IsInFlight reports whether an issue is currently being processed.
// Used by the webhook handler to avoid duplicate processing.
func (s *Scanner) IsInFlight(key string) bool {
	s.inFlightMu.Lock()
	defer s.inFlightMu.Unlock()
	_, exists := s.inFlight[key]
	return exists
}

func (s *Scanner) buildJQL() string {
	projects := make([]string, len(s.cfg.Jira.ProjectKeys))
	for i, k := range s.cfg.Jira.ProjectKeys {
		projects[i] = fmt.Sprintf("%q", k)
	}

	return fmt.Sprintf(
		"project IN (%s) AND issuetype = Bug AND statusCategory != Done ORDER BY key ASC",
		strings.Join(projects, ", "),
	)
}

// Verify Processor implements IssueProcessor at compile time.
var _ IssueProcessor = (*triage.Processor)(nil)
