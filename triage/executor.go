package triage

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"go.uber.org/zap"

	"triage-bot/config"
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
}

// Executor invokes the AI CLI to run a triage assessment.
type Executor struct {
	cfg      config.Config
	tmpl     *template.Template
	logger   *zap.Logger
	execFn   func(name string, args ...string) *exec.Cmd
}

func NewExecutor(cfg config.Config, logger *zap.Logger) (*Executor, error) {
	tmpl, err := loadTemplate(cfg.Triage)
	if err != nil {
		return nil, fmt.Errorf("failed to load task template: %w", err)
	}

	return &Executor{
		cfg:    cfg,
		tmpl:   tmpl,
		logger: logger,
		execFn: exec.Command,
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
func (e *Executor) Run(issueKey, projectKey string) (string, *Metadata, error) {
	outputPath := filepath.Join(outputBase, issueKey+".md")
	metadataPath := filepath.Join(outputBase, issueKey+".meta.json")
	workDir := filepath.Join(workspaceBase, issueKey)

	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("failed to create workspace: %w", err)
	}
	if err := os.MkdirAll(outputBase, 0o755); err != nil {
		return "", nil, fmt.Errorf("failed to create output dir: %w", err)
	}
	defer os.RemoveAll(workDir)
	defer os.Remove(outputPath)
	defer os.Remove(metadataPath)

	data := TemplateData{
		IssueKey:     issueKey,
		IssueURL:     fmt.Sprintf("%s/browse/%s", strings.TrimRight(e.cfg.Jira.BaseURL, "/"), issueKey),
		OutputPath:   outputPath,
		MetadataPath: metadataPath,
		WorkflowPath: e.cfg.Triage.WorkflowPath,
		ProjectKey:   projectKey,
	}

	var buf bytes.Buffer
	if err := e.tmpl.Execute(&buf, data); err != nil {
		return "", nil, fmt.Errorf("failed to render task template: %w", err)
	}

	taskPath := filepath.Join(workDir, "task.md")
	if err := os.WriteFile(taskPath, buf.Bytes(), 0o644); err != nil {
		return "", nil, fmt.Errorf("failed to write task file: %w", err)
	}

	if err := e.runCLI(taskPath, workDir); err != nil {
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

func (e *Executor) runCLI(taskPath, workDir string) error {
	prompt := fmt.Sprintf("Read %s and complete the task described there.", taskPath)

	args := e.buildArgs(prompt)

	e.logger.Info("Invoking AI CLI",
		zap.String("provider", e.cfg.AI.Provider),
		zap.String("model", e.cfg.AI.Model))

	cmd := e.execFn(args[0], args[1:]...)
	cmd.Dir = workDir
	cmd.Env = e.buildEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	timeout := time.Duration(e.cfg.AI.TimeoutMinutes) * time.Minute
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("AI CLI exited with error: %w", err)
		}
		return nil
	case <-time.After(timeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return fmt.Errorf("AI CLI timed out after %v", timeout)
	}
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
