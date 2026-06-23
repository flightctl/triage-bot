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
