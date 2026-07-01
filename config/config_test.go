package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestLoadConfig_EnvVarOverride(t *testing.T) {
	content := `
jira:
  base_url: "https://file.example.com"
  username: "file-user"
  api_token: "file-token"
  project_keys:
    - PROJ
ai:
  provider: claude
  claude:
    api_key: "file-key"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TRIAGE_BOT_JIRA_BASE_URL", "https://env.example.com")
	t.Setenv("TRIAGE_BOT_JIRA_USERNAME", "env-user")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Jira.BaseURL != "https://env.example.com" {
		t.Errorf("base_url = %q, want env override", cfg.Jira.BaseURL)
	}
	if cfg.Jira.Username != "env-user" {
		t.Errorf("username = %q, want env override", cfg.Jira.Username)
	}
	if cfg.Jira.APIToken != "file-token" {
		t.Errorf("api_token = %q, want file value (no env override)", cfg.Jira.APIToken)
	}
}

func TestLoadConfig_NestedEnvVar(t *testing.T) {
	content := `
jira:
  base_url: "https://example.com"
  username: "user"
  api_token: "token"
  project_keys:
    - PROJ
ai:
  provider: claude
  claude:
    api_key: "key"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TRIAGE_BOT_AI_CLAUDE_VERTEX_PROJECT_ID", "my-gcp-project")
	t.Setenv("TRIAGE_BOT_AI_CLAUDE_VERTEX_REGION", "us-east5")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AI.Claude.VertexProjectID != "my-gcp-project" {
		t.Errorf("vertex_project_id = %q, want %q", cfg.AI.Claude.VertexProjectID, "my-gcp-project")
	}
	if cfg.AI.Claude.VertexRegion != "us-east5" {
		t.Errorf("vertex_region = %q, want %q", cfg.AI.Claude.VertexRegion, "us-east5")
	}
}

func TestLoadConfig_InvalidProjectKey(t *testing.T) {
	content := `
jira:
  base_url: "https://example.com"
  username: "user"
  api_token: "token"
  project_keys:
    - "invalid-key"
ai:
  provider: claude
  claude:
    api_key: "key"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid project key")
	}
	if !strings.Contains(err.Error(), "not a valid Jira project key") {
		t.Errorf("error = %q, want project key validation error", err.Error())
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

func TestLoadConfig_SourceDefaults(t *testing.T) {
	content := `
jira: {base_url: "http://x", username: "u", api_token: "t", project_keys: ["PROJ"]}
ai: {provider: claude, claude: {api_key: "sk-test"}}
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Source.BaseDir != "/var/lib/triage-bot/repos" {
		t.Errorf("source.base_dir = %q, want default", cfg.Source.BaseDir)
	}
	if len(cfg.Source.Projects) != 0 {
		t.Errorf("source.projects = %v, want empty", cfg.Source.Projects)
	}
}

func TestLoadConfig_SourceSingleRepo(t *testing.T) {
	content := `
jira: {base_url: "http://x", username: "u", api_token: "t", project_keys: ["PROJ"]}
ai: {provider: claude, claude: {api_key: "sk-test"}}
source:
  projects:
    PROJ:
      repos:
        - url: https://github.com/org/proj.git
          ref: main
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	proj, ok := cfg.Source.Projects["PROJ"]
	if !ok {
		t.Fatal("expected PROJ in source.projects")
	}
	if len(proj.Repos) != 1 {
		t.Fatalf("repos count = %d, want 1", len(proj.Repos))
	}
	if proj.Repos[0].URL != "https://github.com/org/proj.git" {
		t.Errorf("repo url = %q", proj.Repos[0].URL)
	}
}

func TestLoadConfig_SourceMultiRepo(t *testing.T) {
	content := `
jira: {base_url: "http://x", username: "u", api_token: "t", project_keys: ["PROJ"]}
ai: {provider: claude, claude: {api_key: "sk-test"}}
source:
  projects:
    PROJ:
      root_repo: https://github.com/org/workspace.git
      repos:
        - name: backend
          url: https://github.com/org/backend.git
        - name: frontend
          url: https://github.com/org/frontend.git
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	proj := cfg.Source.Projects["PROJ"]
	if proj.RootRepo != "https://github.com/org/workspace.git" {
		t.Errorf("root_repo = %q", proj.RootRepo)
	}
	if len(proj.Repos) != 2 {
		t.Fatalf("repos count = %d, want 2", len(proj.Repos))
	}
}

func TestLoadConfig_SourceValidation(t *testing.T) {
	base := `
jira: {base_url: "http://x", username: "u", api_token: "t", project_keys: ["PROJ"]}
ai: {provider: claude, claude: {api_key: "sk-test"}}
`
	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name: "missing base_dir",
			source: `
source:
  base_dir: ""
  projects:
    PROJ:
      repos:
        - url: https://github.com/org/repo.git
`,
			wantErr: "source.base_dir is required",
		},
		{
			name: "no repos in project",
			source: `
source:
  projects:
    PROJ:
      repos: []
`,
			wantErr: "source.projects.PROJ: at least one repo is required",
		},
		{
			name: "empty repo URL",
			source: `
source:
  projects:
    PROJ:
      repos:
        - url: ""
`,
			wantErr: "source.projects.PROJ.repos[0]: url is required",
		},
		{
			name: "multi-repo missing name",
			source: `
source:
  projects:
    PROJ:
      repos:
        - name: backend
          url: https://github.com/org/backend.git
        - url: https://github.com/org/frontend.git
`,
			wantErr: "source.projects.PROJ.repos[1]: name is required for multi-repo projects",
		},
		{
			name: "multi-repo duplicate name",
			source: `
source:
  projects:
    PROJ:
      repos:
        - name: backend
          url: https://github.com/org/backend.git
        - name: backend
          url: https://github.com/org/other.git
`,
			wantErr: "source.projects.PROJ: duplicate repo name \"backend\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(base+tt.source), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}
