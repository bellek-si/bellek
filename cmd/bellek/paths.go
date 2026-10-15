package main

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
)

const maxPathsPerCall = 32

var editorTools = []string{"Edit", "Write", "MultiEdit", "NotebookEdit"}

func touchedPaths(tool string, raw json.RawMessage, cwd string, ignore []string, skip func(string) bool, roots func() []string) []string {
	if !slices.Contains(editorTools, tool) {
		return nil
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil
	}
	return relativePaths(editorPaths(input), cwd, ignore, skip, roots)
}

func relativePaths(paths []string, cwd string, ignore []string, skip func(string) bool, roots func() []string) []string {
	worktrees := func() []string { return nil }
	if roots != nil {
		worktrees = sync.OnceValue(roots)
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if skip != nil && skip(p) {
			continue
		}
		rel, ok := relativeTo(cwd, p)
		if ok && strings.Contains(rel, "/") {
			rel = deepest(worktrees(), p, rel)
		}
		if !ok {
			rel, ok = relativeToAny(worktrees(), p)
		}
		if !ok || seen[rel] || ignored(rel, ignore) {
			continue
		}
		seen[rel] = true
		out = append(out, rel)
	}
	sort.Strings(out)
	if len(out) > maxPathsPerCall {
		out = out[:maxPathsPerCall]
	}
	return out
}

// memoryPaths lists the memory fact files a file tool wrote, in input order.
func memoryPaths(raw json.RawMessage, isMemory func(string) bool) []string {
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range editorPaths(input) {
		if isMemory(p) && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// editorPaths reads the file a file tool names, plus the ones a multi edit lists.
func editorPaths(input map[string]any) []string {
	var out []string
	for _, key := range []string{"file_path", "notebook_path"} {
		if p, ok := input[key].(string); ok {
			out = append(out, p)
		}
	}
	edits, _ := input["edits"].([]any)
	for _, e := range edits {
		m, _ := e.(map[string]any)
		if p, ok := m["file_path"].(string); ok {
			out = append(out, p)
		}
	}
	return out
}

// relativeTo returns p relative to root. A relative p is taken as already relative to root.
// Symlinks are resolved on both sides because git prints /private/var where macOS gives /var.
func relativeTo(root, p string) (string, bool) {
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		if p == ".." || strings.HasPrefix(p, "../") {
			return "", false
		}
		return p, true
	}
	if root == "" {
		return "", false
	}
	rel, err := filepath.Rel(resolved(root), resolved(p))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// deepest keys a path under cwd by a worktree nested inside cwd, such as .claude/worktrees/x,
// so a file has the same key from every checkout.
func deepest(roots []string, p, rel string) string {
	for _, root := range roots {
		if r, ok := relativeTo(root, p); ok && len(r) < len(rel) {
			rel = r
		}
	}
	return rel
}

func relativeToAny(roots []string, p string) (string, bool) {
	for _, root := range roots {
		if rel, ok := relativeTo(root, p); ok {
			return rel, true
		}
	}
	return "", false
}

// resolved follows symlinks on the deepest existing ancestor of p, so a file about to be
// written under a symlinked directory still resolves.
func resolved(p string) string {
	rest := ""
	for dir := p; ; dir = filepath.Dir(dir) {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

// ignored matches without regard to case, because on a case insensitive file
// system SUB/.ENV and sub/.env are the same file.
func ignored(p string, globs []string) bool {
	p = strings.ToLower(p)
	base := filepath.Base(p)
	for _, g := range globs {
		g = strings.ToLower(g)
		if ok, _ := filepath.Match(g, base); ok {
			return true
		}
		if ok, _ := filepath.Match(g, p); ok {
			return true
		}
	}
	return false
}
