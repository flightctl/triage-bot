package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"triage-bot/config"
	"triage-bot/jira"
	"triage-bot/scanner"
	"triage-bot/server"
	"triage-bot/triage"
	"triage-bot/workflow"
)

func main() {
	configPath := flag.String("config", "", "path to config.yaml")
	flag.Parse()

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger := initLogger(cfg.Logging)
	defer logger.Sync()

	logger.Info("Starting triage-bot",
		zap.String("provider", cfg.AI.Provider),
		zap.String("model", cfg.AI.Model),
		zap.Strings("projects", cfg.Jira.ProjectKeys),
		zap.Int("interval_seconds", cfg.Jira.IntervalSeconds))

	if err := setupMCPConfig(cfg, logger); err != nil {
		logger.Fatal("Failed to setup MCP config", zap.Error(err))
	}

	imp := workflow.NewImporter(cfg.Triage.Import, logger)
	if err := imp.Import(); err != nil {
		logger.Fatal("Failed to import workflow", zap.Error(err))
	}

	jiraClient := jira.NewClient(cfg.Jira.BaseURL, cfg.Jira.Username, cfg.Jira.APIToken, logger)

	executor, err := triage.NewExecutor(*cfg, logger)
	if err != nil {
		logger.Fatal("Failed to create executor", zap.Error(err))
	}

	processor := triage.NewProcessor(jiraClient, executor, *cfg, logger)

	ctx, cancel := context.WithCancel(context.Background())

	inFlight := scanner.NewInFlight()
	s := scanner.NewScanner(jiraClient, processor, inFlight, *cfg, logger)

	var webhookHandler *server.WebhookHandler
	if cfg.Server.WebhookSecret != "" {
		var err error
		webhookHandler, err = server.NewWebhookHandler(processor, inFlight, ctx, cfg.Server.WebhookSecret, cfg.AI.MaxConcurrent, logger)
		if err != nil {
			logger.Fatal("Failed to create webhook handler", zap.Error(err))
		}
		logger.Info("Webhook endpoint enabled at /webhook")
	} else {
		logger.Info("Webhook endpoint disabled (no webhook_secret configured)")
	}
	srv := server.NewServer(cfg.Server.Port, webhookHandler, logger)
	if err := srv.Start(); err != nil {
		logger.Fatal("Failed to start server", zap.Error(err))
	}

	s.Start(ctx)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	logger.Info("Shutdown signal received")
	cancel()
	s.Stop()
	if webhookHandler != nil {
		webhookHandler.Wait()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server shutdown error", zap.Error(err))
	}

	logger.Info("Triage-bot stopped")
}

func initLogger(cfg config.LoggingConfig) *zap.Logger {
	var zapCfg zap.Config
	if cfg.Format == "console" {
		zapCfg = zap.NewDevelopmentConfig()
	} else {
		zapCfg = zap.NewProductionConfig()
	}

	switch cfg.Level {
	case "debug":
		zapCfg.Level.SetLevel(zapcore.DebugLevel)
	case "warn":
		zapCfg.Level.SetLevel(zapcore.WarnLevel)
	case "error":
		zapCfg.Level.SetLevel(zapcore.ErrorLevel)
	default:
		zapCfg.Level.SetLevel(zapcore.InfoLevel)
	}

	logger, err := zapCfg.Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	return logger
}

// setupMCPConfig merges MCP server config into ~/.claude.json so the
// Claude Code CLI can discover Jira tools during triage assessments.
func setupMCPConfig(cfg *config.Config, logger *zap.Logger) error {
	if cfg.MCP.Jira.Command == "" {
		logger.Info("No MCP Jira server configured, skipping MCP setup")
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	configPath := filepath.Join(home, ".claude.json")
	if err := writeMCPConfig(cfg, configPath); err != nil {
		return err
	}

	logger.Info("Wrote Claude Code MCP settings", zap.String("path", configPath))
	return nil
}

// writeMCPConfig merges the Jira MCP server entry into the Claude Code
// config file at configPath, preserving any existing keys.
// Must be called before any CLI subprocesses are spawned (non-atomic
// read-modify-write is safe only at startup).
func writeMCPConfig(cfg *config.Config, configPath string) error {
	env := make(map[string]string)
	for k, v := range cfg.MCP.Jira.Env {
		env[k] = v
	}

	populateIfEmpty(env, "ATLASSIAN_SITE_NAME", cfg.Jira.SiteName)
	populateIfEmpty(env, "ATLASSIAN_USER_EMAIL", cfg.Jira.Username)
	populateIfEmpty(env, "ATLASSIAN_API_TOKEN", cfg.Jira.APIToken)

	existing := make(map[string]any)
	if data, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("failed to parse existing %s: %w", configPath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing %s: %w", configPath, err)
	}

	mcpServers, _ := existing["mcpServers"].(map[string]any)
	if mcpServers == nil {
		mcpServers = make(map[string]any)
	}

	mcpServers["jira"] = map[string]any{
		"type":    "stdio",
		"command": cfg.MCP.Jira.Command,
		"args":    cfg.MCP.Jira.Args,
		"env":     env,
	}
	existing["mcpServers"] = mcpServers

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(configPath, data, 0o600)
}

func populateIfEmpty(env map[string]string, key, value string) {
	if env[key] == "" && value != "" {
		env[key] = value
	}
}
