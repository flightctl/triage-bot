package scanner

import (
	"testing"

	"triage-bot/config"
)

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
