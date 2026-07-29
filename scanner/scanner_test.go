package scanner

import (
	"context"
	"fmt"
	"testing"

	"go.uber.org/zap"

	"triage-bot/config"
	"triage-bot/jira"
)

// mockJiraClient implements ScannerJiraClient for testing.
type mockJiraClient struct {
	searchResults *jira.JiraSearchResponse
	searchErr     error
	searchCalls   int
	addLabelCalls []labelCall
	addLabelErr   error
}

type labelCall struct {
	key   string
	label string
}

func (m *mockJiraClient) SearchTickets(_ context.Context, _ string, _ int, _ string) (*jira.JiraSearchResponse, error) {
	m.searchCalls++
	return m.searchResults, m.searchErr
}

func (m *mockJiraClient) AddLabel(_ context.Context, key, label string) error {
	m.addLabelCalls = append(m.addLabelCalls, labelCall{key, label})
	return m.addLabelErr
}

func TestBuildJQL(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Config
		wantJQL string
	}{
		{
			name: "no excluded components",
			cfg: config.Config{
				Jira: config.JiraConfig{
					ProjectKeys: []string{"OSAC"},
				},
			},
			wantJQL: `project IN ("OSAC") AND issuetype = Bug AND statusCategory != Done ORDER BY key ASC`,
		},
		{
			name: "one excluded component",
			cfg: config.Config{
				Jira: config.JiraConfig{
					ProjectKeys:        []string{"OSAC"},
					ExcludedComponents: []string{"Enclave"},
				},
			},
			wantJQL: `project IN ("OSAC") AND issuetype = Bug AND statusCategory != Done AND (component is EMPTY OR component NOT IN ("Enclave")) ORDER BY key ASC`,
		},
		{
			name: "multiple excluded components",
			cfg: config.Config{
				Jira: config.JiraConfig{
					ProjectKeys:        []string{"OSAC"},
					ExcludedComponents: []string{"Enclave", "Docs"},
				},
			},
			wantJQL: `project IN ("OSAC") AND issuetype = Bug AND statusCategory != Done AND (component is EMPTY OR component NOT IN ("Enclave", "Docs")) ORDER BY key ASC`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Scanner{cfg: tt.cfg}
			got := s.buildJQL()
			if got != tt.wantJQL {
				t.Errorf("buildJQL() =\n  %s\nwant:\n  %s", got, tt.wantJQL)
			}
		})
	}
}

func TestBuildInactiveJQL(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Config
		wantJQL string
	}{
		{
			name: "single project 14 days",
			cfg: config.Config{
				Jira: config.JiraConfig{
					ProjectKeys: []string{"OSAC"},
				},
				Triage: config.TriageConfig{
					StaleLabel:  "jira-triage-stale",
					StaleDays:   14,
					StaleStatus: "New",
				},
			},
			wantJQL: `project IN ("OSAC") AND issuetype = Bug AND status = "New" AND updated <= "-14d" ORDER BY key ASC`,
		},
		{
			name: "single project with excluded components",
			cfg: config.Config{
				Jira: config.JiraConfig{
					ProjectKeys:        []string{"OSAC"},
					ExcludedComponents: []string{"Enclave", "Docs"},
				},
				Triage: config.TriageConfig{
					StaleLabel:  "jira-triage-stale",
					StaleDays:   14,
					StaleStatus: "New",
				},
			},
			wantJQL: `project IN ("OSAC") AND issuetype = Bug AND status = "New" AND updated <= "-14d" AND (component is EMPTY OR component NOT IN ("Enclave", "Docs")) ORDER BY key ASC`,
		},
		{
			name: "custom status",
			cfg: config.Config{
				Jira: config.JiraConfig{
					ProjectKeys: []string{"EDM"},
				},
				Triage: config.TriageConfig{
					StaleLabel:  "stale",
					StaleDays:   7,
					StaleStatus: "Open",
				},
			},
			wantJQL: `project IN ("EDM") AND issuetype = Bug AND status = "Open" AND updated <= "-7d" ORDER BY key ASC`,
		},
		{
			name: "multiple projects custom days",
			cfg: config.Config{
				Jira: config.JiraConfig{
					ProjectKeys: []string{"OSAC", "OTHER"},
				},
				Triage: config.TriageConfig{
					StaleLabel:  "stale",
					StaleDays:   30,
					StaleStatus: "New",
				},
			},
			wantJQL: `project IN ("OSAC", "OTHER") AND issuetype = Bug AND status = "New" AND updated <= "-30d" ORDER BY key ASC`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Scanner{cfg: tt.cfg}
			got := s.buildInactiveJQL()
			if got != tt.wantJQL {
				t.Errorf("buildInactiveJQL() =\n  %s\nwant:\n  %s", got, tt.wantJQL)
			}
		})
	}
}

func TestScanInactive(t *testing.T) {
	baseCfg := config.Config{
		Jira: config.JiraConfig{
			ProjectKeys: []string{"OSAC"},
			MaxResults:  100,
		},
		Triage: config.TriageConfig{
			StaleLabel:  "jira-triage-stale",
			StaleDays:   14,
			StaleStatus: "New",
		},
	}

	t.Run("skips when stale_label is empty", func(t *testing.T) {
		mock := &mockJiraClient{}
		cfg := baseCfg
		cfg.Triage.StaleLabel = ""
		s := &Scanner{jiraClient: mock, cfg: cfg, logger: zap.NewNop()}

		s.scanInactive(context.Background())

		if mock.searchCalls != 0 {
			t.Error("expected no Jira calls when stale_label is empty")
		}
	})

	t.Run("skips when stale_days is zero", func(t *testing.T) {
		mock := &mockJiraClient{}
		cfg := baseCfg
		cfg.Triage.StaleDays = 0
		s := &Scanner{jiraClient: mock, cfg: cfg, logger: zap.NewNop()}

		s.scanInactive(context.Background())

		if mock.searchCalls != 0 {
			t.Error("expected no Jira calls when stale_days is 0")
		}
	})

	t.Run("skips when stale_days is negative", func(t *testing.T) {
		mock := &mockJiraClient{}
		cfg := baseCfg
		cfg.Triage.StaleDays = -1
		s := &Scanner{jiraClient: mock, cfg: cfg, logger: zap.NewNop()}

		s.scanInactive(context.Background())

		if mock.searchCalls != 0 {
			t.Error("expected no Jira calls when stale_days is negative")
		}
	})

	t.Run("adds stale label to inactive issues", func(t *testing.T) {
		mock := &mockJiraClient{
			searchResults: &jira.JiraSearchResponse{
				Issues: []jira.JiraIssue{{Key: "OSAC-500"}, {Key: "OSAC-501"}},
				IsLast: true,
			},
		}
		s := &Scanner{jiraClient: mock, cfg: baseCfg, logger: zap.NewNop()}

		s.scanInactive(context.Background())

		if len(mock.addLabelCalls) != 2 {
			t.Fatalf("expected 2 AddLabel calls, got %d", len(mock.addLabelCalls))
		}
		if mock.addLabelCalls[0].label != "jira-triage-stale" || mock.addLabelCalls[1].label != "jira-triage-stale" {
			t.Errorf("unexpected labels: %+v", mock.addLabelCalls)
		}
	})

	t.Run("dry run does not mutate", func(t *testing.T) {
		mock := &mockJiraClient{
			searchResults: &jira.JiraSearchResponse{
				Issues: []jira.JiraIssue{{Key: "OSAC-600"}},
				IsLast: true,
			},
		}
		cfg := baseCfg
		cfg.DryRun = true
		s := &Scanner{jiraClient: mock, cfg: cfg, logger: zap.NewNop()}

		s.scanInactive(context.Background())

		if len(mock.addLabelCalls) > 0 {
			t.Error("expected no label mutations in dry run")
		}
	})

	t.Run("search error aborts gracefully", func(t *testing.T) {
		mock := &mockJiraClient{searchErr: fmt.Errorf("connection refused")}
		s := &Scanner{jiraClient: mock, cfg: baseCfg, logger: zap.NewNop()}

		s.scanInactive(context.Background())

		if len(mock.addLabelCalls) > 0 {
			t.Error("expected no label calls when search fails")
		}
	})

	t.Run("continues on AddLabel error", func(t *testing.T) {
		mock := &mockJiraClient{
			searchResults: &jira.JiraSearchResponse{
				Issues: []jira.JiraIssue{{Key: "OSAC-700"}, {Key: "OSAC-701"}},
				IsLast: true,
			},
			addLabelErr: fmt.Errorf("503 Service Unavailable"),
		}
		s := &Scanner{jiraClient: mock, cfg: baseCfg, logger: zap.NewNop()}

		s.scanInactive(context.Background())

		if len(mock.addLabelCalls) != 2 {
			t.Errorf("expected 2 AddLabel attempts, got %d", len(mock.addLabelCalls))
		}
	})
}
