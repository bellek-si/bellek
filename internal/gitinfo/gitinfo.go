package gitinfo

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bellek-si/bellek/api"
	"github.com/google/uuid"
)

const repoIDKey = "bellek.repoId"

func Remote(ctx context.Context, cwd string) string {
	return api.RepoAddress(output(ctx, cwd, "config", "--get", "remote.origin.url"))
}

func output(ctx context.Context, cwd string, args ...string) string {
	if cwd == "" {
		return ""
	}
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// RepoID is the id NewRepoID wrote into the repository's own git config, which
// every worktree shares and no clone copies.
func RepoID(ctx context.Context, cwd string) string {
	id, _ := api.CanonicalUUID(output(ctx, cwd, "config", "--local", "--get", repoIDKey))
	return id
}

// NewRepoID answers the id another call wrote first, so two sessions that
// start together agree on one.
func NewRepoID(ctx context.Context, cwd string) string {
	if id := RepoID(ctx, cwd); id != "" {
		return id
	}
	if output(ctx, cwd, "rev-parse", "--is-inside-work-tree") != "true" {
		return ""
	}
	_ = exec.CommandContext(ctx, "git", "-C", cwd, "config", "--local", repoIDKey, uuid.NewString()).Run()
	return RepoID(ctx, cwd)
}

// RepoName is the folder name of the main worktree, the only part of a local
// path that names the repository.
func RepoName(ctx context.Context, cwd string) string {
	roots := Worktrees(ctx, cwd)
	if len(roots) == 0 {
		return ""
	}
	return filepath.Base(roots[0])
}

func Branch(ctx context.Context, cwd string) string {
	return output(ctx, cwd, "rev-parse", "--abbrev-ref", "HEAD")
}

func DiffStat(ctx context.Context, cwd string) string {
	return output(ctx, cwd, "diff", "--stat")
}

// git lists the main worktree first.
func Worktrees(ctx context.Context, cwd string) []string {
	var roots []string
	for _, line := range strings.Split(output(ctx, cwd, "worktree", "list", "--porcelain"), "\n") {
		if root, ok := strings.CutPrefix(line, "worktree "); ok {
			roots = append(roots, root)
		}
	}
	return roots
}

// Status answers nil when git fails and an empty list for a clean repository, so a
// caller can tell them apart.
func Status(ctx context.Context, cwd string) []string {
	root := output(ctx, cwd, "rev-parse", "--show-toplevel")
	if root == "" {
		return nil
	}
	out, err := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all").Output()
	if err != nil {
		return nil
	}
	paths := []string{}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		paths = append(paths, filepath.Join(root, entry[3:]))
		if (entry[0] == 'R' || entry[0] == 'C') && i+1 < len(fields) {
			i++
			paths = append(paths, filepath.Join(root, fields[i]))
		}
	}
	slices.Sort(paths)
	return paths
}
