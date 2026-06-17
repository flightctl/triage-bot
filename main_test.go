package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"triage-bot/config"
)

func TestWriteMCPConfig_NewFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".claude.json")

	cfg := &config.Config{}
	cfg.MCP.Jira.Command = "npx"
	cfg.MCP.Jira.Args = []string{"-y", "@aashari/mcp-server-atlassian-jira"}
	cfg.Jira.SiteName = "mysite"
	cfg.Jira.Username = "user@example.com"
	cfg.Jira.APIToken = "tok123"
	cfg.Jira.BaseURL = "https://mysite.atlassian.net"

	if err := writeMCPConfig(cfg, configPath); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}

	servers, ok := result["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("mcpServers key missing or wrong type")
	}

	jira, ok := servers["jira"].(map[string]any)
	if !ok {
		t.Fatal("jira server missing")
	}

	if jira["type"] != "stdio" {
		t.Errorf("type = %v, want stdio", jira["type"])
	}
	if jira["command"] != "npx" {
		t.Errorf("command = %v, want npx", jira["command"])
	}

	env, ok := jira["env"].(map[string]any)
	if !ok {
		t.Fatal("env missing")
	}
	if env["ATLASSIAN_SITE_NAME"] != "mysite" {
		t.Errorf("ATLASSIAN_SITE_NAME = %v, want mysite", env["ATLASSIAN_SITE_NAME"])
	}
	if env["ATLASSIAN_USER_EMAIL"] != "user@example.com" {
		t.Errorf("ATLASSIAN_USER_EMAIL = %v", env["ATLASSIAN_USER_EMAIL"])
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config file mode = %o, want 600", got)
	}
}

func TestWriteMCPConfig_MergesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".claude.json")

	existing := map[string]any{
		"firstStartTime":   "2026-01-01T00:00:00Z",
		"migrationVersion": 13,
		"mcpServers": map[string]any{
			"other-server": map[string]any{
				"type":    "stdio",
				"command": "other-cmd",
			},
		},
	}
	data, err := json.Marshal(existing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.MCP.Jira.Command = "npx"
	cfg.MCP.Jira.Args = []string{"-y", "mcp-jira"}
	cfg.Jira.SiteName = "test"

	if err := writeMCPConfig(cfg, configPath); err != nil {
		t.Fatal(err)
	}

	data, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}

	if result["firstStartTime"] != "2026-01-01T00:00:00Z" {
		t.Error("existing key firstStartTime was lost")
	}
	if result["migrationVersion"] != float64(13) {
		t.Error("existing key migrationVersion was lost")
	}

	servers, ok := result["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("mcpServers key missing or wrong type")
	}
	if _, ok := servers["other-server"]; !ok {
		t.Error("existing MCP server other-server was lost")
	}
	if _, ok := servers["jira"]; !ok {
		t.Error("jira MCP server was not added")
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config file mode = %o, want 600", got)
	}
}

func TestWriteMCPConfig_ExplicitEnvWins(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".claude.json")

	cfg := &config.Config{}
	cfg.MCP.Jira.Command = "npx"
	cfg.MCP.Jira.Args = []string{"-y", "mcp-jira"}
	cfg.MCP.Jira.Env = map[string]string{
		"ATLASSIAN_SITE_NAME": "explicit-site",
	}
	cfg.Jira.SiteName = "auto-site"

	if err := writeMCPConfig(cfg, configPath); err != nil {
		t.Fatal(err)
	}

	data, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}

	servers, ok := result["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("mcpServers key missing or wrong type")
	}
	jira, ok := servers["jira"].(map[string]any)
	if !ok {
		t.Fatal("jira server missing or wrong type")
	}
	env, ok := jira["env"].(map[string]any)
	if !ok {
		t.Fatal("env key missing or wrong type")
	}

	if env["ATLASSIAN_SITE_NAME"] != "explicit-site" {
		t.Errorf("explicit env should win, got %v", env["ATLASSIAN_SITE_NAME"])
	}
}

func TestWriteMCPConfig_InvalidJSONReturnsError(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".claude.json")

	if err := os.WriteFile(configPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.MCP.Jira.Command = "npx"
	cfg.MCP.Jira.Args = []string{"-y", "mcp-jira"}

	err := writeMCPConfig(cfg, configPath)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestWriteMCPConfig_UnreadableFileReturnsError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses file permission checks")
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".claude.json")

	if err := os.WriteFile(configPath, []byte("{}"), 0o000); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.MCP.Jira.Command = "npx"
	cfg.MCP.Jira.Args = []string{"-y", "mcp-jira"}

	err := writeMCPConfig(cfg, configPath)
	if err == nil {
		t.Fatal("expected error for unreadable file, got nil")
	}
}
