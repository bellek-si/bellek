package gitinfo

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bellek-si/bellek/api"
)

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
