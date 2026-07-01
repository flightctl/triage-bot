package source

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"go.uber.org/zap"

	"triage-bot/config"
)

var validIssueKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]+-\d+$`)

// Manager handles cloning and updating source repositories for projects.
// Clones are lazy (first access per project key) and shared across issues.
// Each issue gets an isolated git worktree so the AI can't dirty the shared clone.
type Manager struct {
	cfg    config.SourceConfig
	logger *zap.Logger
	mu     sync.Mutex
	clones map[string]*cloneState

	// execFn allows stubbing exec.CommandContext in tests.
	execFn func(ctx context.Context, name string, args ...string) *exec.Cmd
}

type cloneState struct {
	mu     sync.Mutex
	cloned bool
	path   string
}

func NewManager(cfg config.SourceConfig, logger *zap.Logger) *Manager {
	return &Manager{
		cfg:    cfg,
		logger: logger,
		clones: make(map[string]*cloneState),
		execFn: exec.CommandContext,
	}
}

// HasProject reports whether a source repo is configured for the given project key.
func (m *Manager) HasProject(projectKey string) bool {
	_, ok := m.cfg.Projects[projectKey]
	return ok
}

// EnsureCloned clones or updates repos for a project key.
// The first call per project clones; subsequent calls fetch and update.
// Returns ("", nil) if no source config exists for the project.
// Clone failures are retryable — a transient error does not permanently
// disable source-level triage for the project. The per-project mutex
// serializes clone and update operations; concurrent issues for the same
// project queue here but run their AI CLI invocations in parallel.
func (m *Manager) EnsureCloned(ctx context.Context, projectKey string) (string, error) {
	projCfg, ok := m.cfg.Projects[projectKey]
	if !ok {
		return "", nil
	}

	state := m.getOrCreateState(projectKey)
	state.mu.Lock()
	defer state.mu.Unlock()

	if !state.cloned {
		path, err := m.cloneProject(ctx, projectKey, projCfg)
		if err != nil {
			return "", err
		}
		state.path = path
		state.cloned = true
	} else {
		// Update failures are non-fatal: the existing clone is still valid,
		// and the AI gets slightly stale code rather than no triage at all.
		_ = m.updateProject(ctx, projectKey, projCfg, state.path)
	}

	return state.path, nil
}

// Worktree creates a disposable git worktree for a specific issue.
// Returns the worktree path and a cleanup function that removes it.
func (m *Manager) Worktree(ctx context.Context, projectKey, issueKey string) (string, func(), error) {
	projCfg, ok := m.cfg.Projects[projectKey]
	if !ok {
		return "", nil, fmt.Errorf("no source config for project %s", projectKey)
	}

	if !validIssueKey.MatchString(issueKey) {
		return "", nil, fmt.Errorf("invalid issue key %q", issueKey)
	}
	worktreeBase := filepath.Join(m.cfg.BaseDir, ".worktrees", issueKey)
	if err := os.MkdirAll(worktreeBase, 0o750); err != nil {
		return "", nil, fmt.Errorf("failed to create worktree base: %w", err)
	}

	projectDir := filepath.Join(m.cfg.BaseDir, filepath.Clean(projectKey))
	isMultiRepo := len(projCfg.Repos) > 1

	if isMultiRepo {
		if err := m.createMultiRepoWorktrees(ctx, projCfg, projectDir, worktreeBase); err != nil {
			_ = os.RemoveAll(worktreeBase) // best-effort cleanup on failure
			return "", nil, err
		}
	} else {
		clonePath := projectDir
		if err := m.createWorktree(ctx, clonePath, worktreeBase, refOrDefault(projCfg.Repos[0].Ref)); err != nil {
			_ = os.RemoveAll(worktreeBase) // best-effort cleanup on failure
			return "", nil, err
		}
	}

	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if isMultiRepo {
			for _, repo := range projCfg.Repos {
				clonePath := filepath.Join(projectDir, repo.Name)
				wtPath := filepath.Join(worktreeBase, repo.Name)
				m.removeWorktree(cleanupCtx, clonePath, wtPath)
			}
		} else {
			m.removeWorktree(cleanupCtx, projectDir, worktreeBase)
		}
		_ = os.RemoveAll(worktreeBase) // best-effort cleanup
	}

	return worktreeBase, cleanup, nil
}

func (m *Manager) getOrCreateState(projectKey string) *cloneState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.clones[projectKey]
	if !ok {
		state = &cloneState{}
		m.clones[projectKey] = state
	}
	return state
}

func (m *Manager) cloneProject(ctx context.Context, projectKey string, projCfg config.SourceProjectConfig) (string, error) {
	projectDir := filepath.Join(m.cfg.BaseDir, filepath.Clean(projectKey))
	isMultiRepo := len(projCfg.Repos) > 1

	if isMultiRepo {
		if err := os.MkdirAll(projectDir, 0o750); err != nil {
			return "", fmt.Errorf("failed to create project dir: %w", err)
		}

		if projCfg.RootRepo != "" {
			m.logger.Info("Cloning root repo",
				zap.String("project", projectKey))
			if err := m.cloneRepo(ctx, projCfg.RootRepo, projectDir, refOrDefault(projCfg.RootRepoRef)); err != nil {
				return "", fmt.Errorf("failed to clone root repo: %w", err)
			}
		}

		for _, repo := range projCfg.Repos {
			dest := filepath.Join(projectDir, filepath.Clean(repo.Name))
			m.logger.Info("Cloning sub-repo",
				zap.String("project", projectKey),
				zap.String("name", repo.Name))
			if err := m.cloneRepo(ctx, repo.URL, dest, refOrDefault(repo.Ref)); err != nil {
				return "", fmt.Errorf("failed to clone repo %s: %w", repo.Name, err)
			}
		}
	} else {
		repo := projCfg.Repos[0]
		m.logger.Info("Cloning source repo",
			zap.String("project", projectKey))
		if err := m.cloneRepo(ctx, repo.URL, projectDir, refOrDefault(repo.Ref)); err != nil {
			return "", fmt.Errorf("failed to clone repo: %w", err)
		}
	}

	return projectDir, nil
}

func (m *Manager) updateProject(ctx context.Context, projectKey string, projCfg config.SourceProjectConfig, projectDir string) error {
	if len(projCfg.Repos) > 1 {
		for _, repo := range projCfg.Repos {
			dest := filepath.Join(projectDir, filepath.Clean(repo.Name))
			if err := m.updateRepo(ctx, dest, refOrDefault(repo.Ref)); err != nil {
				m.logger.Warn("Failed to update sub-repo",
					zap.String("project", projectKey),
					zap.String("name", repo.Name),
					zap.Error(err))
			}
		}
	} else {
		if err := m.updateRepo(ctx, projectDir, refOrDefault(projCfg.Repos[0].Ref)); err != nil {
			m.logger.Warn("Failed to update source repo",
				zap.String("project", projectKey),
				zap.Error(err))
		}
	}
	return nil
}

func (m *Manager) cloneRepo(ctx context.Context, repoURL, dest, ref string) error {
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		return m.updateRepo(ctx, dest, ref)
	}

	cmd := m.execFn(ctx, "git", "clone", "--depth", "1", "--branch", ref, "--", repoURL, dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		m.logger.Debug("git clone output", zap.String("output", string(out)))
		return fmt.Errorf("git clone failed: %w", err)
	}
	return nil
}

func (m *Manager) updateRepo(ctx context.Context, dest, ref string) error {
	fetch := m.execFn(ctx, "git", "-C", dest, "fetch", "origin")
	if out, err := fetch.CombinedOutput(); err != nil {
		m.logger.Debug("git fetch output", zap.String("output", string(out)))
		return fmt.Errorf("git fetch failed: %w", err)
	}

	checkout := m.execFn(ctx, "git", "-C", dest, "checkout", "origin/"+ref)
	if out, err := checkout.CombinedOutput(); err != nil {
		m.logger.Debug("git checkout output", zap.String("output", string(out)))
		return fmt.Errorf("git checkout failed: %w", err)
	}
	return nil
}

func (m *Manager) createWorktree(ctx context.Context, clonePath, worktreePath, ref string) error {
	cmd := m.execFn(ctx, "git", "-C", clonePath, "worktree", "add", worktreePath, "origin/"+ref)
	if out, err := cmd.CombinedOutput(); err != nil {
		m.logger.Debug("git worktree add output", zap.String("output", string(out)))
		return fmt.Errorf("git worktree add failed: %w", err)
	}
	return nil
}

func (m *Manager) createMultiRepoWorktrees(ctx context.Context, projCfg config.SourceProjectConfig, projectDir, worktreeBase string) error {
	for _, repo := range projCfg.Repos {
		clonePath := filepath.Join(projectDir, repo.Name)
		wtPath := filepath.Join(worktreeBase, repo.Name)
		if err := m.createWorktree(ctx, clonePath, wtPath, refOrDefault(repo.Ref)); err != nil {
			return fmt.Errorf("failed to create worktree for %s: %w", repo.Name, err)
		}
	}
	return nil
}

func (m *Manager) removeWorktree(ctx context.Context, clonePath, worktreePath string) {
	cmd := m.execFn(ctx, "git", "-C", clonePath, "worktree", "remove", "--force", worktreePath)
	if err := cmd.Run(); err != nil {
		m.logger.Debug("Failed to remove worktree (best-effort)",
			zap.String("path", worktreePath),
			zap.Error(err))
	}
}

func refOrDefault(ref string) string {
	if ref == "" {
		return "main"
	}
	return ref
}
