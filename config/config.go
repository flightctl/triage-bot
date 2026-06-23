package config

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/viper"
)

var validProjectKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)

type Config struct {
	Server  ServerConfig  `mapstructure:"server"`
	Logging LoggingConfig `mapstructure:"logging"`
	Jira    JiraConfig    `mapstructure:"jira"`
	AI      AIConfig      `mapstructure:"ai"`
	Triage  TriageConfig  `mapstructure:"triage"`
	MCP     MCPConfig     `mapstructure:"mcp"`
	DryRun  bool          `mapstructure:"dry_run"`
}

type ServerConfig struct {
	Port          int    `mapstructure:"port"`
	WebhookSecret string `mapstructure:"webhook_secret"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

type JiraConfig struct {
	BaseURL            string   `mapstructure:"base_url"`
	SiteName           string   `mapstructure:"site_name"`
	Username           string   `mapstructure:"username"`
	APIToken           string   `mapstructure:"api_token"`
	BotUsername        string   `mapstructure:"bot_username"`
	IntervalSeconds    int      `mapstructure:"interval_seconds"`
	ProjectKeys        []string `mapstructure:"project_keys"`
	MaxResults         int      `mapstructure:"max_results"`
	ExcludedComponents []string `mapstructure:"excluded_components"`
}

type AIConfig struct {
	Provider       string       `mapstructure:"provider"`
	Model          string       `mapstructure:"model"`
	MaxConcurrent  int          `mapstructure:"max_concurrent"`
	TimeoutMinutes int          `mapstructure:"timeout_minutes"`
	Claude         ClaudeConfig `mapstructure:"claude"`
	Gemini         GeminiConfig `mapstructure:"gemini"`
	AllowedTools   string       `mapstructure:"allowed_tools"`
}

type ClaudeConfig struct {
	APIKey                string `mapstructure:"api_key"`
	VertexProjectID       string `mapstructure:"vertex_project_id"`
	VertexRegion          string `mapstructure:"vertex_region"`
	VertexCredentialsFile string `mapstructure:"vertex_credentials_file"`
}

type GeminiConfig struct {
	APIKey string `mapstructure:"api_key"`
}

type TriageConfig struct {
	WorkflowPath     string       `mapstructure:"workflow_path"`
	AutoFixLabel     string       `mapstructure:"auto_fix_label"`
	AutoFixThreshold int          `mapstructure:"auto_fix_threshold"`
	MissingInfoLabel string       `mapstructure:"missing_info_label"`
	NotFixableLabel  string       `mapstructure:"not_fixable_label"`
	TaskTemplatePath string       `mapstructure:"task_template_path"`
	TaskTemplate     string       `mapstructure:"task_template"`
	Import           ImportConfig `mapstructure:"import"`
}

type ImportConfig struct {
	Repo string `mapstructure:"repo"`
	Path string `mapstructure:"path"`
	Ref  string `mapstructure:"ref"`
	Dest string `mapstructure:"dest"`
}

type MCPConfig struct {
	Jira MCPServerConfig `mapstructure:"jira"`
}

type MCPServerConfig struct {
	Command string            `mapstructure:"command"`
	Args    []string          `mapstructure:"args"`
	Env     map[string]string `mapstructure:"env"`
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.port", 8080)
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("jira.interval_seconds", 300)
	v.SetDefault("jira.max_results", 100)
	v.SetDefault("ai.provider", "claude")
	v.SetDefault("ai.model", "claude-sonnet-4-6")
	v.SetDefault("ai.max_concurrent", 3)
	v.SetDefault("ai.timeout_minutes", 15)
	v.SetDefault("ai.allowed_tools", "")
	v.SetDefault("triage.workflow_path", "/opt/workflows/triage")
	v.SetDefault("triage.auto_fix_label", "ai-autofix-candidate")
	v.SetDefault("triage.auto_fix_threshold", 80)
	v.SetDefault("triage.missing_info_label", "triage-missing-info")
	v.SetDefault("triage.not_fixable_label", "triage-not-fixable")
	v.SetDefault("triage.import.ref", "main")
}

func LoadConfig(configPath string) (*Config, error) {
	v := viper.New()

	setDefaults(v)

	v.SetEnvPrefix("TRIAGE_BOT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	bindEnv := func(key string) {
		if err := v.BindEnv(key); err != nil {
			panic(fmt.Sprintf("failed to bind environment variable %s: %v", key, err))
		}
	}

	bindEnv("dry_run")
	bindEnv("server.port")
	bindEnv("server.webhook_secret")

	bindEnv("logging.level")
	bindEnv("logging.format")

	bindEnv("jira.base_url")
	bindEnv("jira.site_name")
	bindEnv("jira.username")
	bindEnv("jira.api_token")
	bindEnv("jira.bot_username")
	bindEnv("jira.interval_seconds")
	bindEnv("jira.max_results")
	bindEnv("jira.excluded_components")

	bindEnv("ai.provider")
	bindEnv("ai.model")
	bindEnv("ai.max_concurrent")
	bindEnv("ai.timeout_minutes")
	bindEnv("ai.allowed_tools")
	bindEnv("ai.claude.api_key")
	bindEnv("ai.claude.vertex_project_id")
	bindEnv("ai.claude.vertex_region")
	bindEnv("ai.claude.vertex_credentials_file")
	bindEnv("ai.gemini.api_key")

	bindEnv("triage.workflow_path")
	bindEnv("triage.auto_fix_label")
	bindEnv("triage.auto_fix_threshold")
	bindEnv("triage.missing_info_label")
	bindEnv("triage.not_fixable_label")
	bindEnv("triage.task_template_path")
	bindEnv("triage.task_template")
	bindEnv("triage.import.repo")
	bindEnv("triage.import.path")
	bindEnv("triage.import.ref")
	bindEnv("triage.import.dest")

	if configPath != "" {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			if _, ok := err.(viper.ConfigFileNotFoundError); ok {
				fmt.Printf("Warning: Config file %s not found, using environment variables and defaults\n", configPath)
			} else {
				return nil, fmt.Errorf("error reading config file: %w", err)
			}
		}
	}

	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if err := config.normalizeAndValidate(); err != nil {
		return nil, err
	}

	return &config, nil
}

func (c *Config) normalizeAndValidate() error {
	c.Jira.BaseURL = strings.TrimRight(c.Jira.BaseURL, "/")
	c.Jira.APIToken = strings.TrimSpace(c.Jira.APIToken)
	c.Jira.Username = strings.TrimSpace(c.Jira.Username)

	if c.Jira.BaseURL == "" {
		return fmt.Errorf("jira.base_url is required")
	}
	if c.Jira.Username == "" {
		return fmt.Errorf("jira.username is required")
	}
	if c.Jira.APIToken == "" {
		return fmt.Errorf("jira.api_token is required")
	}
	if len(c.Jira.ProjectKeys) == 0 {
		return fmt.Errorf("jira.project_keys must contain at least one project key")
	}
	for _, key := range c.Jira.ProjectKeys {
		if !validProjectKey.MatchString(key) {
			return fmt.Errorf("jira.project_keys: %q is not a valid Jira project key (must match %s)", key, validProjectKey.String())
		}
	}
	if c.Jira.BotUsername == "" {
		c.Jira.BotUsername = c.Jira.Username
	}

	switch c.AI.Provider {
	case "claude":
		hasDirectKey := c.AI.Claude.APIKey != ""
		hasVertex := c.AI.Claude.VertexProjectID != ""
		if !hasDirectKey && !hasVertex {
			return fmt.Errorf("claude requires either api_key or vertex_project_id")
		}
		if hasVertex && c.AI.Claude.VertexRegion == "" {
			return fmt.Errorf("claude.vertex_region is required when using Vertex AI")
		}
	case "gemini":
		if c.AI.Gemini.APIKey == "" {
			return fmt.Errorf("gemini.api_key is required when using Gemini provider")
		}
	default:
		return fmt.Errorf("unsupported AI provider: %s (must be claude or gemini)", c.AI.Provider)
	}

	if c.AI.MaxConcurrent < 1 {
		c.AI.MaxConcurrent = 1
	}
	if c.AI.TimeoutMinutes < 1 {
		c.AI.TimeoutMinutes = 15
	}

	if c.Triage.Import.Dest == "" {
		c.Triage.Import.Dest = c.Triage.WorkflowPath
	}

	return nil
}

// UseVertexAI returns true if Claude should authenticate via Vertex AI.
func (c *Config) UseVertexAI() bool {
	return c.AI.Provider == "claude" && c.AI.Claude.VertexProjectID != ""
}
