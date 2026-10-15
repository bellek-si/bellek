package gitinfo_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bellek-si/bellek/internal/gitinfo"
	"github.com/stretchr/testify/require"
)

func newRepo(t *testing.T) (string, func(args ...string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "--quiet")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "test")
	return repo, git
}

func TestBranch(t *testing.T) {
	ctx := context.Background()
	repo, git := newRepo(t)
	git("checkout", "--quiet", "-b", "topic")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o600))
	git("add", "a.txt")
	git("commit", "--quiet", "-m", "first")

	require.Equal(t, "topic", gitinfo.Branch(ctx, repo))
	require.Empty(t, gitinfo.Branch(ctx, ""), "an empty cwd has no branch")
	require.Empty(t, gitinfo.Branch(ctx, t.TempDir()), "a directory outside a repository has no branch")
	require.Empty(t, gitinfo.Branch(ctx, filepath.Join(repo, "missing")), "a missing directory has no branch")

	dead, cancel := context.WithCancel(ctx)
	cancel()
	require.Empty(t, gitinfo.Branch(dead, repo), "a spent context ends the git call instead of the hook's second")
	require.Empty(t, gitinfo.Remote(dead, repo))
	require.Empty(t, gitinfo.DiffStat(dead, repo))
	require.Empty(t, gitinfo.Worktrees(dead, repo))
}

func TestDiffStatSummarisesTheChangedTrackedFiles(t *testing.T) {
	repo, git := newRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o600))
	git("add", "a.txt")
	git("commit", "--quiet", "-m", "first")

	ctx := context.Background()
	require.Empty(t, gitinfo.DiffStat(ctx, repo), "a clean tree has nothing to summarise")

	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("b\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0o600))
	stat := gitinfo.DiffStat(ctx, repo)
	require.Contains(t, stat, "a.txt |")
	require.NotContains(t, stat, "new.txt", "an untracked file is not in the diff")
	require.Empty(t, gitinfo.DiffStat(ctx, ""), "an empty cwd has no diff")
}

func TestWorktrees(t *testing.T) {
	repo, git := newRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o600))
	git("add", "a.txt")
	git("commit", "--quiet", "-m", "first")

	ctx := context.Background()
	require.Equal(t, []string{resolve(t, repo)}, resolveAll(t, gitinfo.Worktrees(ctx, repo)))

	linked := filepath.Join(t.TempDir(), "feature")
	git("worktree", "add", "--quiet", "-b", "feature", linked)
	roots := resolveAll(t, gitinfo.Worktrees(ctx, repo))
	require.Equal(t, []string{resolve(t, repo), resolve(t, linked)}, roots, "the main worktree comes first")
	require.Equal(t, roots, resolveAll(t, gitinfo.Worktrees(ctx, linked)), "the list is the same seen from the linked worktree")

	require.Nil(t, gitinfo.Worktrees(ctx, ""), "an empty cwd has no worktrees")
	require.Nil(t, gitinfo.Worktrees(ctx, t.TempDir()), "a directory outside a repository has no worktrees")
	require.Nil(t, gitinfo.Worktrees(ctx, filepath.Join(repo, "missing")), "a missing directory has no worktrees")
}

func resolve(t *testing.T, p string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return got
}

func resolveAll(t *testing.T, paths []string) []string {
	t.Helper()
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, resolve(t, p))
	}
	return out
}

func TestRemoteDropsTheCredentialsAClonePutInTheURL(t *testing.T) {
	repo, git := newRepo(t)
	git("config", "remote.origin.url", "https://x-access-token:secret-for-test@github.com/o/r.git")
	require.Equal(t, "https://github.com/o/r.git", gitinfo.Remote(context.Background(), repo))
}

func TestStatus(t *testing.T) {
	repo, git := newRepo(t)
	for _, name := range []string{"a.txt", "old.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte(name), 0o600))
	}
	git("add", ".")
	git("commit", "--quiet", "-m", "first")

	ctx := context.Background()
	require.Equal(t, []string{}, gitinfo.Status(ctx, repo), "a clean repository answers with an empty list, not nil")

	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("changed"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "sub", "new file.txt"), []byte("new"), 0o600))
	git("mv", "old.txt", "renamed.txt")
	root := resolve(t, repo)
	want := []string{
		filepath.Join(root, "a.txt"),
		filepath.Join(root, "old.txt"),
		filepath.Join(root, "renamed.txt"),
		filepath.Join(root, "sub", "new file.txt"),
	}
	require.Equal(t, want, gitinfo.Status(ctx, filepath.Join(repo, "sub")), "paths are absolute from any directory in the repository")

	require.Nil(t, gitinfo.Status(ctx, ""), "an empty cwd has no status")
	require.Nil(t, gitinfo.Status(ctx, t.TempDir()), "a directory outside a repository has no status")

	require.NoError(t, os.WriteFile(filepath.Join(repo, ".git", "index"), []byte("not an index"), 0o600))
	require.Nil(t, gitinfo.Status(ctx, repo), "a status git cannot read is nil, not a clean tree")
}
