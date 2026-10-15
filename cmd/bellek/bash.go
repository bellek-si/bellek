package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const snapshotMaxAge = 24 * time.Hour

type fileStamp struct {
	Size    int64 `json:"size"`
	ModTime int64 `json:"modTime"`
}

type bashSnapshot struct {
	Stamps    map[string]fileStamp `json:"stamps"`
	Worktrees []string             `json:"worktrees"`
}

func (r *hookRunner) preToolUse(ctx context.Context, in hookInput) error {
	if in.ToolName != "Bash" || !r.cfg.Captures("files") {
		return nil
	}
	if _, ok := r.state.Load(in.SessionID); !ok {
		return nil
	}
	dirty := r.status(ctx, in.Cwd)
	if dirty == nil {
		return nil
	}
	snap := bashSnapshot{Stamps: stamps(dirty), Worktrees: r.worktrees(ctx, in.Cwd)}
	if err := r.state.SaveSnapshot(in.SessionID, in.ToolUseID, snap); err != nil {
		r.warn("pre-tool-use", "bash snapshot not written: %v", err)
	}
	return nil
}

func (r *hookRunner) bashChanges(ctx context.Context, in hookInput) []string {
	before, ok := r.state.TakeSnapshot(in.SessionID, in.ToolUseID)
	if !ok {
		return nil
	}
	dirty := r.status(ctx, in.Cwd)
	if dirty == nil {
		return nil
	}
	after := stamps(dirty)
	var out []string
	for p, stamp := range after {
		if was, ok := before.Stamps[p]; !ok || was != stamp {
			out = append(out, p)
		}
	}
	for p := range before.Stamps {
		if _, ok := after[p]; !ok {
			out = append(out, p)
		}
	}
	now := r.worktrees(ctx, in.Cwd)
	if before.Worktrees != nil {
		for _, wt := range now {
			if !slices.Contains(before.Worktrees, wt) {
				out = append(out, r.status(ctx, wt)...)
			}
		}
	}
	// A worktree created inside the checkout shows in its git status as an untracked folder.
	return slices.DeleteFunc(out, func(p string) bool { return slices.Contains(now, p) })
}

func stamps(paths []string) map[string]fileStamp {
	out := make(map[string]fileStamp, len(paths))
	for _, p := range paths {
		info, err := os.Lstat(p)
		if err != nil {
			out[p] = fileStamp{Size: -1}
			continue
		}
		out[p] = fileStamp{Size: info.Size(), ModTime: info.ModTime().UnixNano()}
	}
	return out
}

func (s *hookState) snapshotPath(claudeID, toolUseID string) (string, bool) {
	if _, ok := s.path(claudeID); !ok || toolUseID == "" || strings.ContainsAny(toolUseID, "/\\") || strings.Contains(toolUseID, "..") {
		return "", false
	}
	return filepath.Join(s.bash(), claudeID+"-"+toolUseID+".json"), true
}

func (s *hookState) SaveSnapshot(claudeID, toolUseID string, snap bashSnapshot) error {
	p, ok := s.snapshotPath(claudeID, toolUseID)
	if !ok {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(s.bash(), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return os.WriteFile(p, raw, 0o600)
}

func (s *hookState) TakeSnapshot(claudeID, toolUseID string) (bashSnapshot, bool) {
	p, ok := s.snapshotPath(claudeID, toolUseID)
	if !ok {
		return bashSnapshot{}, false
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return bashSnapshot{}, false
	}
	_ = os.Remove(p)
	var snap bashSnapshot
	if json.Unmarshal(raw, &snap) != nil {
		return bashSnapshot{}, false
	}
	return snap, true
}

func (s *hookState) PruneSnapshots() {
	entries, err := os.ReadDir(s.bash())
	if err != nil {
		return
	}
	cutoff := s.now().Add(-snapshotMaxAge)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(s.bash(), e.Name()))
		}
	}
}
