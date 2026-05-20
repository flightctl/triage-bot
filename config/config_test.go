package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_MinimalValid(t *testing.T) {
	content := `
jira:
  base_url: "https://jira.example.com"
  username: "bot@example.com"
  api_token: "test-token"
  project_keys:
    - PROJ1
ai:
  provider: claude
  claude:
    vertex_project_id: "my-project"
    vertex_region: "us-east5"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Jira.BaseURL != "https://jira.example.com" {
		t.Errorf("base_url = %q, want %q", cfg.Jira.BaseURL, "https://jira.example.com")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.AI.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q, want %q", cfg.AI.Model, "claude-sonnet-4-6")
	}
	if cfg.AI.MaxConcurrent != 3 {
		t.Errorf("max_concurrent = %d, want 3", cfg.AI.MaxConcurrent)
	}
	if cfg.Triage.AutoFixThreshold != 80 {
		t.Errorf("auto_fix_threshold = %d, want 80", cfg.Triage.AutoFixThreshold)
	}
	if cfg.Jira.BotUsername != "bot@example.com" {
		t.Errorf("bot_username = %q, want %q", cfg.Jira.BotUsername, "bot@example.com")
	}
}

func TestLoadConfig_MissingRequired(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "missing base_url",
			content: `jira: {username: "u", api_token: "t", project_keys: ["PROJ"]}`,
			wantErr: "jira.base_url is required",
		},
		{
			name:    "missing project_keys",
			content: `jira: {base_url: "http://x", username: "u", api_token: "t"}`,
			wantErr: "jira.project_keys must contain at least one project key",
		},
		{
			name: "claude without auth",
			content: `
jira: {base_url: "http://x", username: "u", api_token: "t", project_keys: ["PROJ"]}
ai: {provider: claude}`,
			wantErr: "claude requires either api_key or vertex_project_id",
		},
		{
			name: "vertex without region",
			content: `
jira: {base_url: "http://x", username: "u", api_token: "t", project_keys: ["PROJ"]}
ai: {provider: claude, claude: {vertex_project_id: "proj"}}`,
			wantErr: "claude.vertex_region is required when using Vertex AI",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got := err.Error(); got != tt.wantErr {
				t.Errorf("error = %q, want %q", got, tt.wantErr)
			}
		})
	}
}

func TestLoadConfig_UseVertexAI(t *testing.T) {
	content := `
jira: {base_url: "http://x", username: "u", api_token: "t", project_keys: ["PROJ"]}
ai:
  provider: claude
  claude:
    vertex_project_id: "my-project"
    vertex_region: "us-east5"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !cfg.UseVertexAI() {
		t.Error("UseVertexAI() = false, want true")
	}
}

func TestLoadConfig_DirectAPIKey(t *testing.T) {
	content := `
jira: {base_url: "http://x", username: "u", api_token: "t", project_keys: ["PROJ"]}
ai:
  provider: claude
  claude:
    api_key: "sk-test"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.UseVertexAI() {
		t.Error("UseVertexAI() = true, want false")
	}
}

func TestLoadConfig_ImportDestDefault(t *testing.T) {
	content := `
jira: {base_url: "http://x", username: "u", api_token: "t", project_keys: ["PROJ"]}
ai: {provider: claude, claude: {api_key: "sk-test"}}
triage:
  workflow_path: /custom/path
  import:
    repo: "https://github.com/org/repo.git"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Triage.Import.Dest != "/custom/path" {
		t.Errorf("import.dest = %q, want %q", cfg.Triage.Import.Dest, "/custom/path")
	}
}
