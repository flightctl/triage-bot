package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap/zaptest"

	"triage-bot/config"
)

// initBareRepo creates a bare git repo with one commit, suitable for cloning.
func initBareRepo(t *testing.T, ref string) string {
	t.Helper()

	work := filepath.Join(t.TempDir(), "work")
	bare := filepath.Join(t.TempDir(), "bare")

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...) // #nosec G204 -- test helper
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %s", args, out)
		}
	}

	run("git", "init", "--initial-branch", ref, work)
	run("git", "-C", work, "config", "user.email", "test@test.com")
	run("git", "-C", work, "config", "user.name", "Test")

	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run("git", "-C", work, "add", ".")
	run("git", "-C", work, "commit", "-m", "init")
	run("git", "clone", "--bare", work, bare)

	return bare
}

func TestHasProject(t *testing.T) {
	m := NewManager(config.SourceConfig{
		Projects: map[string]config.SourceProjectConfig{
			"PROJ": {},
		},
	}, zaptest.NewLogger(t))

	if !m.HasProject("PROJ") {
		t.Error("HasProject(PROJ) = false, want true")
	}
	if m.HasProject("OTHER") {
		t.Error("HasProject(OTHER) = true, want false")
	}
}

func TestEnsureCloned_NoConfig(t *testing.T) {
	m := NewManager(config.SourceConfig{}, zaptest.NewLogger(t))

	path, err := m.EnsureCloned(context.Background(), "PROJ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "" {
		t.Errorf("path = %q, want empty", path)
	}
}

func TestEnsureCloned_SingleRepo(t *testing.T) {
	bare := initBareRepo(t, "main")
	baseDir := filepath.Join(t.TempDir(), "repos")

	m := NewManager(config.SourceConfig{
		BaseDir: baseDir,
		Projects: map[string]config.SourceProjectConfig{
			"PROJ": {
				Repos: []config.SourceRepoEntry{
					{URL: bare, Ref: "main"},
				},
			},
		},
	}, zaptest.NewLogger(t))

	path, err := m.EnsureCloned(context.Background(), "PROJ")
	if err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}

	if want := filepath.Join(baseDir, "PROJ"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}

	if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
		t.Errorf("README.md not found in clone: %v", err)
	}
}

func TestEnsureCloned_MultiRepo(t *testing.T) {
	bareA := initBareRepo(t, "main")
	bareB := initBareRepo(t, "main")
	baseDir := filepath.Join(t.TempDir(), "repos")

	m := NewManager(config.SourceConfig{
		BaseDir: baseDir,
		Projects: map[string]config.SourceProjectConfig{
			"MULTI": {
				Repos: []config.SourceRepoEntry{
					{Name: "svc-a", URL: bareA, Ref: "main"},
					{Name: "svc-b", URL: bareB, Ref: "main"},
				},
			},
		},
	}, zaptest.NewLogger(t))

	path, err := m.EnsureCloned(context.Background(), "MULTI")
	if err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}

	for _, name := range []string{"svc-a", "svc-b"} {
		readme := filepath.Join(path, name, "README.md")
		if _, err := os.Stat(readme); err != nil {
			t.Errorf("%s/README.md not found: %v", name, err)
		}
	}
}

func TestEnsureCloned_SubsequentCallUpdates(t *testing.T) {
	bare := initBareRepo(t, "main")
	baseDir := filepath.Join(t.TempDir(), "repos")

	m := NewManager(config.SourceConfig{
		BaseDir: baseDir,
		Projects: map[string]config.SourceProjectConfig{
			"PROJ": {
				Repos: []config.SourceRepoEntry{
					{URL: bare, Ref: "main"},
				},
			},
		},
	}, zaptest.NewLogger(t))

	path1, err := m.EnsureCloned(context.Background(), "PROJ")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	path2, err := m.EnsureCloned(context.Background(), "PROJ")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if path1 != path2 {
		t.Errorf("paths differ: %q vs %q", path1, path2)
	}
}

func TestEnsureCloned_MultiRepoWithRootRepo(t *testing.T) {
	bareRoot := initBareRepo(t, "main")
	bareSub := initBareRepo(t, "main")
	baseDir := filepath.Join(t.TempDir(), "repos")

	m := NewManager(config.SourceConfig{
		BaseDir: baseDir,
		Projects: map[string]config.SourceProjectConfig{
			"PROJ": {
				RootRepo: bareRoot,
				Repos: []config.SourceRepoEntry{
					{Name: "sub", URL: bareSub, Ref: "main"},
					{Name: "other", URL: bareSub, Ref: "main"},
				},
			},
		},
	}, zaptest.NewLogger(t))

	path, err := m.EnsureCloned(context.Background(), "PROJ")
	if err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}

	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		t.Error("root repo .git not found")
	}
	if _, err := os.Stat(filepath.Join(path, "sub", "README.md")); err != nil {
		t.Error("sub/README.md not found")
	}
}

func TestWorktree_SingleRepo(t *testing.T) {
	bare := initBareRepo(t, "main")
	baseDir := filepath.Join(t.TempDir(), "repos")

	m := NewManager(config.SourceConfig{
		BaseDir: baseDir,
		Projects: map[string]config.SourceProjectConfig{
			"PROJ": {
				Repos: []config.SourceRepoEntry{
					{URL: bare, Ref: "main"},
				},
			},
		},
	}, zaptest.NewLogger(t))

	ctx := context.Background()
	if _, err := m.EnsureCloned(ctx, "PROJ"); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}

	wtPath, cleanup, err := m.Worktree(ctx, "PROJ", "PROJ-123")
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wtPath, "README.md")); err != nil {
		t.Errorf("README.md not in worktree: %v", err)
	}

	cleanup()

	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree not cleaned up: %v", err)
	}
}

func TestWorktree_MultiRepo(t *testing.T) {
	bareA := initBareRepo(t, "main")
	bareB := initBareRepo(t, "main")
	baseDir := filepath.Join(t.TempDir(), "repos")

	m := NewManager(config.SourceConfig{
		BaseDir: baseDir,
		Projects: map[string]config.SourceProjectConfig{
			"PROJ": {
				Repos: []config.SourceRepoEntry{
					{Name: "svc-a", URL: bareA, Ref: "main"},
					{Name: "svc-b", URL: bareB, Ref: "main"},
				},
			},
		},
	}, zaptest.NewLogger(t))

	ctx := context.Background()
	if _, err := m.EnsureCloned(ctx, "PROJ"); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}

	wtPath, cleanup, err := m.Worktree(ctx, "PROJ", "PROJ-456")
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}

	for _, name := range []string{"svc-a", "svc-b"} {
		if _, err := os.Stat(filepath.Join(wtPath, name, "README.md")); err != nil {
			t.Errorf("%s/README.md not in worktree: %v", name, err)
		}
	}

	cleanup()

	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree not cleaned up: %v", err)
	}
}

func TestWorktree_NoConfig(t *testing.T) {
	m := NewManager(config.SourceConfig{}, zaptest.NewLogger(t))

	_, _, err := m.Worktree(context.Background(), "PROJ", "PROJ-1")
	if err == nil {
		t.Fatal("expected error for unconfigured project")
	}
}

func TestRefOrDefault(t *testing.T) {
	if got := refOrDefault(""); got != "main" {
		t.Errorf("refOrDefault empty = %q, want main", got)
	}
	if got := refOrDefault("develop"); got != "develop" {
		t.Errorf("refOrDefault develop = %q", got)
	}
}

func TestEnsureCloned_BadURL(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "repos")

	m := NewManager(config.SourceConfig{
		BaseDir: baseDir,
		Projects: map[string]config.SourceProjectConfig{
			"PROJ": {
				Repos: []config.SourceRepoEntry{
					{URL: "https://invalid.example.com/no-such-repo.git", Ref: "main"},
				},
			},
		},
	}, zaptest.NewLogger(t))

	_, err := m.EnsureCloned(context.Background(), "PROJ")
	if err == nil {
		t.Fatal("expected error for bad repo URL")
	}
	if !strings.Contains(err.Error(), "git clone failed") {
		t.Errorf("error = %q, want git clone failure", err.Error())
	}
}

func TestEnsureCloned_RetryAfterFailure(t *testing.T) {
	bare := initBareRepo(t, "main")
	baseDir := filepath.Join(t.TempDir(), "repos")

	callCount := 0
	m := NewManager(config.SourceConfig{
		BaseDir: baseDir,
		Projects: map[string]config.SourceProjectConfig{
			"PROJ": {
				Repos: []config.SourceRepoEntry{
					{URL: bare, Ref: "main"},
				},
			},
		},
	}, zaptest.NewLogger(t))

	// Stub execFn: first git-clone call fails, all subsequent calls succeed.
	realExec := m.execFn
	m.execFn = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		callCount++
		if callCount == 1 {
			return exec.CommandContext(ctx, "false") // exit 1
		}
		return realExec(ctx, name, args...)
	}

	ctx := context.Background()

	_, err := m.EnsureCloned(ctx, "PROJ")
	if err == nil {
		t.Fatal("first call should fail")
	}

	// Second call retries the clone with the same config — should succeed.
	path, err := m.EnsureCloned(ctx, "PROJ")
	if err != nil {
		t.Fatalf("retry should succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
		t.Errorf("README.md not found after retry: %v", err)
	}
}
