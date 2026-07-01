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
	removeCalls   []labelCall
	removeErr     error
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

func (m *mockJiraClient) RemoveLabel(_ context.Context, key, label string) error {
	m.removeCalls = append(m.removeCalls, labelCall{key, label})
	return m.removeErr
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
			wantJQL: `project IN ("OSAC") AND issuetype = Bug AND statusCategory != Done AND component NOT IN ("Enclave") ORDER BY key ASC`,
		},
		{
			name: "multiple excluded components",
			cfg: config.Config{
				Jira: config.JiraConfig{
					ProjectKeys:        []string{"OSAC"},
					ExcludedComponents: []string{"Enclave", "Docs"},
				},
			},
			wantJQL: `project IN ("OSAC") AND issuetype = Bug AND statusCategory != Done AND component NOT IN ("Enclave", "Docs") ORDER BY key ASC`,
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

func TestBuildStaleJQL(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Config
		wantJQL string
	}{
		{
			name: "single project with progression labels",
			cfg: config.Config{
				Jira: config.JiraConfig{
					ProjectKeys: []string{"OSAC"},
				},
				Triage: config.TriageConfig{
					AutoFixLabel: "jira-autofix",
					StaleLabel:   "jira-triage-stale",
					ProgressionLabels: []string{
						"jira-autofix-merged",
						"jira-autofix-rejected",
					},
				},
			},
			wantJQL: `project IN ("OSAC") AND issuetype = Bug AND statusCategory = Done AND labels = "jira-autofix" AND labels NOT IN ("jira-autofix-merged", "jira-autofix-rejected", "jira-triage-stale") ORDER BY key ASC`,
		},
		{
			name: "stale label included in exclusion list prevents re-processing",
			cfg: config.Config{
				Jira: config.JiraConfig{
					ProjectKeys: []string{"PROJ"},
				},
				Triage: config.TriageConfig{
					AutoFixLabel: "autofix",
					StaleLabel:   "stale",
					ProgressionLabels: []string{
						"autofix-merged",
					},
				},
			},
			wantJQL: `project IN ("PROJ") AND issuetype = Bug AND statusCategory = Done AND labels = "autofix" AND labels NOT IN ("autofix-merged", "stale") ORDER BY key ASC`,
		},
		{
			name: "multiple projects",
			cfg: config.Config{
				Jira: config.JiraConfig{
					ProjectKeys: []string{"OSAC", "OTHER"},
				},
				Triage: config.TriageConfig{
					AutoFixLabel:      "jira-autofix",
					StaleLabel:        "jira-triage-stale",
					ProgressionLabels: []string{"jira-autofix-merged"},
				},
			},
			wantJQL: `project IN ("OSAC", "OTHER") AND issuetype = Bug AND statusCategory = Done AND labels = "jira-autofix" AND labels NOT IN ("jira-autofix-merged", "jira-triage-stale") ORDER BY key ASC`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Scanner{cfg: tt.cfg}
			got := s.buildStaleJQL()
			if got != tt.wantJQL {
				t.Errorf("buildStaleJQL() =\n  %s\nwant:\n  %s", got, tt.wantJQL)
			}
		})
	}
}

func TestScanUntriaged(t *testing.T) {
	baseCfg := config.Config{
		Jira: config.JiraConfig{
			ProjectKeys: []string{"OSAC"},
			MaxResults:  100,
		},
		Triage: config.TriageConfig{
			AutoFixLabel:      "jira-autofix",
			MissingInfoLabel:  "jira-triage-missing-info",
			NotFixableLabel:   "jira-triage-not-fixable",
			StaleLabel:        "jira-triage-stale",
			ProgressionLabels: []string{"jira-autofix-merged"},
		},
	}

	t.Run("skips when stale_label is empty", func(t *testing.T) {
		mock := &mockJiraClient{}
		cfg := baseCfg
		cfg.Triage.StaleLabel = ""
		s := &Scanner{jiraClient: mock, cfg: cfg, logger: zap.NewNop()}

		s.scanUntriaged(context.Background())

		if mock.searchCalls != 0 {
			t.Error("expected no Jira calls when stale_label is empty")
		}
	})

	t.Run("adds stale label to untriaged issues", func(t *testing.T) {
		mock := &mockJiraClient{
			searchResults: &jira.JiraSearchResponse{
				Issues: []jira.JiraIssue{{Key: "OSAC-100"}, {Key: "OSAC-101"}},
				IsLast: true,
			},
		}
		s := &Scanner{jiraClient: mock, cfg: baseCfg, logger: zap.NewNop()}

		s.scanUntriaged(context.Background())

		if len(mock.addLabelCalls) != 2 {
			t.Fatalf("expected 2 AddLabel calls, got %d", len(mock.addLabelCalls))
		}
		if mock.addLabelCalls[0].label != "jira-triage-stale" || mock.addLabelCalls[1].label != "jira-triage-stale" {
			t.Errorf("unexpected labels: %+v", mock.addLabelCalls)
		}
		if len(mock.removeCalls) > 0 {
			t.Error("expected no RemoveLabel calls for untriaged scan")
		}
	})

	t.Run("continues on AddLabel error", func(t *testing.T) {
		mock := &mockJiraClient{
			searchResults: &jira.JiraSearchResponse{
				Issues: []jira.JiraIssue{{Key: "OSAC-300"}, {Key: "OSAC-301"}},
				IsLast: true,
			},
			addLabelErr: fmt.Errorf("503 Service Unavailable"),
		}
		s := &Scanner{jiraClient: mock, cfg: baseCfg, logger: zap.NewNop()}

		s.scanUntriaged(context.Background())

		if len(mock.addLabelCalls) != 2 {
			t.Errorf("expected 2 AddLabel attempts, got %d", len(mock.addLabelCalls))
		}
	})

	t.Run("search error aborts gracefully", func(t *testing.T) {
		mock := &mockJiraClient{searchErr: fmt.Errorf("connection refused")}
		s := &Scanner{jiraClient: mock, cfg: baseCfg, logger: zap.NewNop()}

		s.scanUntriaged(context.Background())

		if len(mock.addLabelCalls) > 0 {
			t.Error("expected no label calls when search fails")
		}
	})

	t.Run("dry run does not mutate", func(t *testing.T) {
		mock := &mockJiraClient{
			searchResults: &jira.JiraSearchResponse{
				Issues: []jira.JiraIssue{{Key: "OSAC-200"}},
				IsLast: true,
			},
		}
		cfg := baseCfg
		cfg.DryRun = true
		s := &Scanner{jiraClient: mock, cfg: cfg, logger: zap.NewNop()}

		s.scanUntriaged(context.Background())

		if len(mock.addLabelCalls) > 0 {
			t.Error("expected no label mutations in dry run")
		}
	})
}

func TestBuildUntriagedJQL(t *testing.T) {
	s := &Scanner{cfg: config.Config{
		Jira: config.JiraConfig{ProjectKeys: []string{"OSAC"}},
		Triage: config.TriageConfig{
			AutoFixLabel:      "jira-autofix",
			MissingInfoLabel:  "jira-triage-missing-info",
			NotFixableLabel:   "",
			StaleLabel:        "jira-triage-stale",
			ProgressionLabels: []string{"jira-autofix-merged"},
		},
	}}

	got := s.buildUntriagedJQL([]string{"jira-autofix", "jira-triage-missing-info", "jira-triage-stale", "jira-autofix-merged"})
	want := `project IN ("OSAC") AND issuetype = Bug AND statusCategory = Done AND (labels is EMPTY OR labels NOT IN ("jira-autofix", "jira-triage-missing-info", "jira-triage-stale", "jira-autofix-merged")) ORDER BY key ASC`
	if got != want {
		t.Errorf("buildUntriagedJQL() =\n  %s\nwant:\n  %s", got, want)
	}
}

func TestScanStale(t *testing.T) {
	baseCfg := config.Config{
		Jira: config.JiraConfig{
			ProjectKeys: []string{"OSAC"},
			MaxResults:  100,
		},
		Triage: config.TriageConfig{
			AutoFixLabel:      "jira-autofix",
			StaleLabel:        "jira-triage-stale",
			ProgressionLabels: []string{"jira-autofix-merged"},
		},
	}

	t.Run("skips when stale_label is empty", func(t *testing.T) {
		mock := &mockJiraClient{}
		cfg := baseCfg
		cfg.Triage.StaleLabel = ""
		s := &Scanner{jiraClient: mock, cfg: cfg, logger: zap.NewNop()}

		s.scanStale(context.Background())

		if mock.searchCalls != 0 || len(mock.addLabelCalls) > 0 || len(mock.removeCalls) > 0 {
			t.Error("expected no Jira calls when stale_label is empty")
		}
	})

	t.Run("skips when autofix_label is empty", func(t *testing.T) {
		mock := &mockJiraClient{}
		cfg := baseCfg
		cfg.Triage.AutoFixLabel = ""
		s := &Scanner{jiraClient: mock, cfg: cfg, logger: zap.NewNop()}

		s.scanStale(context.Background())

		if mock.searchCalls != 0 || len(mock.addLabelCalls) > 0 || len(mock.removeCalls) > 0 {
			t.Error("expected no Jira calls when autofix_label is empty")
		}
	})

	t.Run("skips when progression_labels is empty", func(t *testing.T) {
		mock := &mockJiraClient{}
		cfg := baseCfg
		cfg.Triage.ProgressionLabels = nil
		s := &Scanner{jiraClient: mock, cfg: cfg, logger: zap.NewNop()}

		s.scanStale(context.Background())

		if mock.searchCalls != 0 || len(mock.addLabelCalls) > 0 || len(mock.removeCalls) > 0 {
			t.Error("expected no Jira calls when progression_labels is empty")
		}
	})

	t.Run("no issues found", func(t *testing.T) {
		mock := &mockJiraClient{
			searchResults: &jira.JiraSearchResponse{Issues: nil, IsLast: true},
		}
		s := &Scanner{jiraClient: mock, cfg: baseCfg, logger: zap.NewNop()}

		s.scanStale(context.Background())

		if len(mock.addLabelCalls) > 0 {
			t.Error("expected no label calls when no issues found")
		}
	})

	t.Run("marks stale issue", func(t *testing.T) {
		mock := &mockJiraClient{
			searchResults: &jira.JiraSearchResponse{
				Issues: []jira.JiraIssue{{Key: "OSAC-100"}},
				IsLast: true,
			},
		}
		s := &Scanner{jiraClient: mock, cfg: baseCfg, logger: zap.NewNop()}

		s.scanStale(context.Background())

		if len(mock.addLabelCalls) != 1 || mock.addLabelCalls[0].key != "OSAC-100" || mock.addLabelCalls[0].label != "jira-triage-stale" {
			t.Errorf("expected AddLabel(OSAC-100, jira-triage-stale), got %+v", mock.addLabelCalls)
		}
		if len(mock.removeCalls) != 1 || mock.removeCalls[0].key != "OSAC-100" || mock.removeCalls[0].label != "jira-autofix" {
			t.Errorf("expected RemoveLabel(OSAC-100, jira-autofix), got %+v", mock.removeCalls)
		}
	})

	t.Run("dry run does not call Jira", func(t *testing.T) {
		mock := &mockJiraClient{
			searchResults: &jira.JiraSearchResponse{
				Issues: []jira.JiraIssue{{Key: "OSAC-200"}},
				IsLast: true,
			},
		}
		cfg := baseCfg
		cfg.DryRun = true
		s := &Scanner{jiraClient: mock, cfg: cfg, logger: zap.NewNop()}

		s.scanStale(context.Background())

		if len(mock.addLabelCalls) > 0 || len(mock.removeCalls) > 0 {
			t.Error("expected no label mutations in dry run")
		}
	})

	t.Run("partial failure skips remove but continues", func(t *testing.T) {
		mock := &mockJiraClient{
			searchResults: &jira.JiraSearchResponse{
				Issues: []jira.JiraIssue{
					{Key: "OSAC-300"},
					{Key: "OSAC-301"},
				},
				IsLast: true,
			},
			addLabelErr: fmt.Errorf("503 Service Unavailable"),
		}
		s := &Scanner{jiraClient: mock, cfg: baseCfg, logger: zap.NewNop()}

		s.scanStale(context.Background())

		if len(mock.addLabelCalls) != 2 {
			t.Errorf("expected 2 AddLabel attempts, got %d", len(mock.addLabelCalls))
		}
		if len(mock.removeCalls) != 0 {
			t.Errorf("expected 0 RemoveLabel calls when AddLabel fails, got %d", len(mock.removeCalls))
		}
	})
}
