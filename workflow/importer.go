package workflow

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"go.uber.org/zap"

	"triage-bot/config"
)

type Importer struct {
	cfg    config.ImportConfig
	logger *zap.Logger
}

func NewImporter(cfg config.ImportConfig, logger *zap.Logger) *Importer {
	return &Importer{cfg: cfg, logger: logger}
}

// Import clones the workflow repo to the destination directory.
// If cfg.Repo is empty, it assumes workflow files are already in place and returns nil.
// If cfg.Path is set, only that subdirectory is checked out (sparse checkout).
func (i *Importer) Import() error {
	if i.cfg.Repo == "" {
		i.logger.Info("No workflow import configured, assuming files are pre-installed",
			zap.String("dest", i.cfg.Dest))
		return nil
	}

	if _, err := os.Stat(i.cfg.Dest); err == nil {
		i.logger.Info("Workflow directory already exists, skipping import",
			zap.String("dest", i.cfg.Dest))
		return nil
	}

	ref := i.cfg.Ref
	if ref == "" {
		ref = "main"
	}

	i.logger.Info("Importing workflow",
		zap.String("repo", i.cfg.Repo),
		zap.String("path", i.cfg.Path),
		zap.String("ref", ref),
		zap.String("dest", i.cfg.Dest))

	if i.cfg.Path == "" {
		return i.cloneFull(ref)
	}
	return i.cloneSparse(ref)
}

func (i *Importer) cloneFull(ref string) error {
	cmd := exec.Command("git", "clone", "--depth", "1", "--branch", ref, "--", i.cfg.Repo, i.cfg.Dest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone failed: %w", err)
	}
	return nil
}

func (i *Importer) cloneSparse(ref string) error {
	tmpDir := i.cfg.Dest + ".tmp"
	_ = os.RemoveAll(tmpDir)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	clone := exec.Command("git", "clone", "--depth", "1", "--branch", ref,
		"--filter=blob:none", "--sparse", "--", i.cfg.Repo, tmpDir)
	clone.Stdout = os.Stdout
	clone.Stderr = os.Stderr
	if err := clone.Run(); err != nil {
		return fmt.Errorf("sparse clone failed: %w", err)
	}

	sparseSet := exec.Command("git", "-C", tmpDir, "sparse-checkout", "set", i.cfg.Path)
	sparseSet.Stdout = os.Stdout
	sparseSet.Stderr = os.Stderr
	if err := sparseSet.Run(); err != nil {
		return fmt.Errorf("sparse-checkout set failed: %w", err)
	}

	src := filepath.Join(tmpDir, i.cfg.Path)
	if err := os.MkdirAll(filepath.Dir(i.cfg.Dest), 0o755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	if err := os.Rename(src, i.cfg.Dest); err != nil {
		return fmt.Errorf("failed to move workflow to dest: %w", err)
	}

	return nil
}
