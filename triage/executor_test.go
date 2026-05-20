package triage

import (
	"testing"

	"triage-bot/config"
)

func TestBuildArgs_Claude(t *testing.T) {
	e := &Executor{
		cfg: config.Config{
			AI: config.AIConfig{
				Provider:     "claude",
				Model:        "claude-sonnet-4-6",
				AllowedTools: "Read Write",
			},
		},
	}

	args := e.buildArgs("do something")

	if args[0] != "claude" {
		t.Errorf("args[0] = %q, want 'claude'", args[0])
	}

	found := map[string]bool{}
	for _, a := range args {
		found[a] = true
	}

	if !found["--dangerously-skip-permissions"] {
		t.Error("missing --dangerously-skip-permissions")
	}
	if !found["--output-format"] {
		t.Error("missing --output-format")
	}
	if !found["claude-sonnet-4-6"] {
		t.Error("missing model")
	}
	if !found["Read Write"] {
		t.Error("missing allowed tools")
	}
}

func TestBuildArgs_Gemini(t *testing.T) {
	e := &Executor{
		cfg: config.Config{
			AI: config.AIConfig{
				Provider: "gemini",
				Model:    "gemini-2.5-pro",
			},
		},
	}

	args := e.buildArgs("do something")

	if args[0] != "gemini" {
		t.Errorf("args[0] = %q, want 'gemini'", args[0])
	}

	for _, a := range args {
		if a == "--dangerously-skip-permissions" {
			t.Error("gemini should not have --dangerously-skip-permissions")
		}
	}
}

func TestBuildArgs_NoAllowedTools(t *testing.T) {
	e := &Executor{
		cfg: config.Config{
			AI: config.AIConfig{
				Provider:     "claude",
				AllowedTools: "",
			},
		},
	}

	args := e.buildArgs("do something")

	for _, a := range args {
		if a == "--allowedTools" {
			t.Error("should not include --allowedTools when empty")
		}
	}
}

func TestBuildEnv_VertexAI(t *testing.T) {
	e := &Executor{
		cfg: config.Config{
			AI: config.AIConfig{
				Provider: "claude",
				Claude: config.ClaudeConfig{
					VertexProjectID:       "my-project",
					VertexRegion:          "us-east5",
					VertexCredentialsFile: "/path/to/key.json",
				},
			},
		},
	}

	env := e.buildEnv()

	envMap := map[string]string{}
	for _, e := range env {
		for i := range e {
			if e[i] == '=' {
				envMap[e[:i]] = e[i+1:]
				break
			}
		}
	}

	if envMap["CLAUDE_CODE_USE_VERTEX"] != "1" {
		t.Error("missing CLAUDE_CODE_USE_VERTEX=1")
	}
	if envMap["CLOUD_ML_PROJECT_ID"] != "my-project" {
		t.Errorf("CLOUD_ML_PROJECT_ID = %q, want 'my-project'", envMap["CLOUD_ML_PROJECT_ID"])
	}
	if envMap["GOOGLE_APPLICATION_CREDENTIALS"] != "/path/to/key.json" {
		t.Errorf("GOOGLE_APPLICATION_CREDENTIALS = %q", envMap["GOOGLE_APPLICATION_CREDENTIALS"])
	}
}

func TestBuildEnv_DirectAPIKey(t *testing.T) {
	e := &Executor{
		cfg: config.Config{
			AI: config.AIConfig{
				Provider: "claude",
				Claude: config.ClaudeConfig{
					APIKey: "sk-test",
				},
			},
		},
	}

	env := e.buildEnv()

	found := false
	for _, e := range env {
		if e == "ANTHROPIC_API_KEY=sk-test" {
			found = true
		}
	}
	if !found {
		t.Error("missing ANTHROPIC_API_KEY")
	}
}
