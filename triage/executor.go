package triage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"go.uber.org/zap"

	"triage-bot/config"
	"triage-bot/source"
)

const (
	workspaceBase = "/tmp/triage-workspace"
	outputBase    = "/tmp/triage-output"
)

// TemplateData is the data available to the task template.
type TemplateData struct {
	IssueKey     string
	IssueURL     string
	OutputPath   string
	MetadataPath string
	WorkflowPath string
	ProjectKey   string
	SourcePath   string
}

// Executor invokes the AI CLI to run a triage assessment.
type Executor struct {
	cfg       config.Config
	tmpl      *template.Template
	sourceMgr *source.Manager
	logger    *zap.Logger
}

// NewExecutor creates an executor. sourceMgr may be nil when no source
// repos are configured.
func NewExecutor(cfg config.Config, sourceMgr *source.Manager, logger *zap.Logger) (*Executor, error) {
	tmpl, err := loadTemplate(cfg.Triage)
	if err != nil {
		return nil, fmt.Errorf("failed to load task template: %w", err)
	}

	return &Executor{
		cfg:       cfg,
		tmpl:      tmpl,
		sourceMgr: sourceMgr,
		logger:    logger,
	}, nil
}

func loadTemplate(cfg config.TriageConfig) (*template.Template, error) {
	if cfg.TaskTemplate != "" {
		return template.New("task").Parse(cfg.TaskTemplate)
	}
	path := cfg.TaskTemplatePath
	if path == "" {
		path = "task.tmpl"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read template file %s: %w", path, err)
	}
	return template.New("task").Parse(string(data))
}

// Run executes the triage assessment for a single issue.
// Returns the markdown assessment text and metadata, or an error.
func (e *Executor) Run(ctx context.Context, issueKey, projectKey string) (string, *Metadata, error) {
	outputPath := filepath.Join(outputBase, issueKey+".md")
	metadataPath := filepath.Join(outputBase, issueKey+".meta.json")
	workspaceDir := filepath.Join(workspaceBase, issueKey)
	workDir := workspaceDir

	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("failed to create workspace: %w", err)
	}
	if err := os.MkdirAll(outputBase, 0o755); err != nil {
		return "", nil, fmt.Errorf("failed to create output dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(workspaceDir) }() // best-effort cleanup
	defer func() { _ = os.Remove(outputPath) }()      // best-effort cleanup
	defer func() { _ = os.Remove(metadataPath) }()    // best-effort cleanup

	data := TemplateData{
		IssueKey:     issueKey,
		IssueURL:     fmt.Sprintf("%s/browse/%s", strings.TrimRight(e.cfg.Jira.BaseURL, "/"), issueKey),
		OutputPath:   outputPath,
		MetadataPath: metadataPath,
		WorkflowPath: e.cfg.Triage.WorkflowPath,
		ProjectKey:   projectKey,
	}

	if e.sourceMgr != nil && e.sourceMgr.HasProject(projectKey) {
		if _, cloneErr := e.sourceMgr.EnsureCloned(ctx, projectKey); cloneErr != nil {
			e.logger.Warn("Source repo unavailable, falling back to Jira-only assessment",
				zap.String("project", projectKey),
				zap.Error(cloneErr))
		} else if wtPath, cleanup, wtErr := e.sourceMgr.Worktree(ctx, projectKey, issueKey); wtErr != nil {
			e.logger.Warn("Failed to create worktree, falling back to Jira-only assessment",
				zap.String("issue", issueKey),
				zap.Error(wtErr))
		} else {
			defer cleanup()
			data.SourcePath = wtPath
			workDir = wtPath
		}
	}

	var buf bytes.Buffer
	if err := e.tmpl.Execute(&buf, data); err != nil {
		return "", nil, fmt.Errorf("failed to render task template: %w", err)
	}

	taskPath := filepath.Join(workDir, "task.md")
	if err := os.WriteFile(taskPath, buf.Bytes(), 0o644); err != nil {
		return "", nil, fmt.Errorf("failed to write task file: %w", err)
	}

	if err := e.runCLI(ctx, taskPath, workDir); err != nil {
		return "", nil, err
	}

	assessment, err := os.ReadFile(outputPath)
	if err != nil {
		return "", nil, fmt.Errorf("AI did not produce assessment file at %s: %w", outputPath, err)
	}

	meta, err := ReadMetadata(metadataPath)
	if err != nil {
		e.logger.Warn("Failed to read metadata sidecar, skipping structured actions",
			zap.String("issue", issueKey),
			zap.Error(err))
		meta = nil
	} else if meta == nil {
		e.logger.Info("No metadata sidecar produced, skipping structured actions",
			zap.String("issue", issueKey))
	}

	return string(assessment), meta, nil
}

func (e *Executor) runCLI(ctx context.Context, taskPath, workDir string) error {
	prompt := fmt.Sprintf("Read %s and complete the task described there.", taskPath)

	args := e.buildArgs(prompt)

	e.logger.Info("Invoking AI CLI",
		zap.String("provider", e.cfg.AI.Provider),
		zap.String("model", e.cfg.AI.Model))

	timeout := time.Duration(e.cfg.AI.TimeoutMinutes) * time.Minute
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = workDir
	cmd.Env = e.buildEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("AI CLI timed out or cancelled after %v: %w", timeout, ctx.Err())
		}
		return fmt.Errorf("AI CLI exited with error: %w", err)
	}
	return nil
}

func (e *Executor) buildArgs(prompt string) []string {
	switch e.cfg.AI.Provider {
	case "gemini":
		args := []string{"gemini", "-y", "--output-format", "json"}
		if e.cfg.AI.Model != "" {
			args = append(args, "--model", e.cfg.AI.Model)
		}
		args = append(args, "-p", prompt)
		return args
	default:
		args := []string{"claude", "--dangerously-skip-permissions", "--output-format", "json"}
		if e.cfg.AI.Model != "" {
			args = append(args, "--model", e.cfg.AI.Model)
		}
		if e.cfg.AI.AllowedTools != "" {
			args = append(args, "--allowedTools", e.cfg.AI.AllowedTools)
		}
		args = append(args, "-p", prompt)
		return args
	}
}

func (e *Executor) buildEnv() []string {
	env := os.Environ()

	if e.cfg.AI.Provider == "claude" {
		if e.cfg.UseVertexAI() {
			env = append(env,
				"CLAUDE_CODE_USE_VERTEX=1",
				"CLOUD_ML_PROJECT_ID="+e.cfg.AI.Claude.VertexProjectID,
				"CLOUD_ML_REGION="+e.cfg.AI.Claude.VertexRegion,
			)
			if e.cfg.AI.Claude.VertexCredentialsFile != "" {
				env = append(env, "GOOGLE_APPLICATION_CREDENTIALS="+e.cfg.AI.Claude.VertexCredentialsFile)
			}
		} else if e.cfg.AI.Claude.APIKey != "" {
			env = append(env, "ANTHROPIC_API_KEY="+e.cfg.AI.Claude.APIKey)
		}
	} else if e.cfg.AI.Provider == "gemini" && e.cfg.AI.Gemini.APIKey != "" {
		env = append(env, "GEMINI_API_KEY="+e.cfg.AI.Gemini.APIKey)
	}

	return env
}
