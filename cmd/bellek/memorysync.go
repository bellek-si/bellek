package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/client"
	"github.com/bellek-si/bellek/internal/markdown"
	"github.com/bellek-si/bellek/internal/redact"
	"github.com/google/uuid"
)

func memoryScope(memoryType, scope string) string {
	switch {
	case scope == markdown.ScopeProject:
		return ""
	case scope == markdown.ScopeGlobal, memoryType == "user", memoryType == "feedback":
		return api.GlobalProjectKey
	}
	return ""
}

func memoryKind(memoryType string) string {
	if memoryType == "feedback" {
		return "decision"
	}
	return "note"
}

// The title is redacted here because it comes from front matter the model
// wrote; the key is not, because the marker in the state directory is keyed by
// the raw name.
func baseNote(id uuid.UUID, name, description, memoryType, scope string, modified time.Time) api.NoteInput {
	// The fallback comes after the pass, because a description that redacts
	// to nothing would otherwise leave an empty title, which Note refuses.
	title := redact.Text(description, 0)
	if strings.TrimSpace(title) == "" {
		title = name
	}
	memoryType = redact.Text(memoryType, 0)
	props := map[string]any{"source": api.MemorySource, "memoryType": memoryType}
	if !modified.IsZero() {
		props["modified"] = modified.UTC().Format(time.RFC3339Nano)
	}
	return api.NoteInput{
		SessionID: id, Key: name, Kind: memoryKind(memoryType), Title: title,
		Tags: []string{"memory", memoryType}, Props: props, ProjectKey: scope,
	}
}

// Both writers build the marker entry here, because a mismatch silently
// re-sends or silently skips.
func entryFor(m markdown.Memory) markerEntry {
	return markerEntry{Modified: m.Modified, Description: m.Description, Type: m.Type, Scope: memoryScope(m.Type, m.Scope)}
}

func memoryNote(id uuid.UUID, m markdown.Memory) api.NoteInput {
	n := baseNote(id, m.Name, m.Description, m.Type, memoryScope(m.Type, m.Scope), m.Modified)
	n.Body = redact.Text(m.Body, api.MaxBodyBytes)
	if m.OriginSessionID != "" {
		n.Props["originSessionId"] = redact.Text(m.OriginSessionID, 0)
	}
	return n
}

// removedNote goes where the name was last sent, which for a marker written before scopes
// existed is the project.
func removedNote(id uuid.UUID, name string, e markerEntry, at time.Time) api.NoteInput {
	return retired(id, name, e, at, "Removed from Claude Code memory on ")
}

func movedNote(id uuid.UUID, name string, e markerEntry, to string, at time.Time) api.NoteInput {
	where := "this project"
	if to == api.GlobalProjectKey {
		where = "global"
	}
	return retired(id, name, e, at, "Moved to "+where+" on ")
}

func retired(id uuid.UUID, name string, e markerEntry, at time.Time, reason string) api.NoteInput {
	n := baseNote(id, name, e.Description, e.Type, e.Scope, at)
	n.Body = reason + at.UTC().Format("2006-01-02") + "."
	n.Tags = append(n.Tags, api.TagRemoved)
	return n
}

// The marker moves only after both notes land, so a failure is retried whole.
func (r *hookRunner) sendMemory(ctx context.Context, id uuid.UUID, marker map[string]markerEntry, m markdown.Memory) error {
	if _, err := r.api.Note(ctx, memoryNote(id, m)); err != nil {
		return err
	}
	next := entryFor(m)
	if e, ok := marker[m.Name]; ok && e.Scope != next.Scope {
		if _, err := r.api.Note(ctx, movedNote(id, m.Name, e, next.Scope, r.state.now())); err != nil && !client.IsCode(err, api.CodeValidation) {
			return err
		}
	}
	marker[m.Name] = next
	return nil
}

// syncMemory sends one memory file the model just wrote and records it in the marker.
func (r *hookRunner) syncMemory(ctx context.Context, id uuid.UUID, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		r.warn("memory", "cannot read %s: %v", path, err)
		return
	}
	m, err := markdown.DecodeMemory(raw)
	if err != nil {
		r.warn("memory", "cannot decode %s: %v", path, err)
		return
	}
	note := memoryNote(id, m)
	if _, err := r.api.Note(ctx, note); err != nil {
		r.spool(spooled{Op: "note", SessionID: id, Note: &note}, err)
		return
	}
	dir := filepath.Dir(path)
	marker := r.state.LoadMarker(dir)
	next := entryFor(m)
	// Each note is spooled on its own, so a move that fails never sends the memory twice.
	if e, ok := marker[m.Name]; ok && e.Scope != next.Scope {
		moved := movedNote(id, m.Name, e, next.Scope, r.state.now())
		if _, err := r.api.Note(ctx, moved); err != nil {
			r.spool(spooled{Op: "note", SessionID: id, Note: &moved}, err)
		}
	}
	marker[m.Name] = next
	if err := r.state.SaveMarker(dir, marker); err != nil {
		r.warn("memory", "marker not saved: %v", err)
	}
}

// walkMemory sends every memory file newer than the marker and a removed version
// for every marker entry whose file is gone. It skips what the server refuses as
// invalid and stops at any other failure.
func (r *hookRunner) walkMemory(ctx context.Context, id uuid.UUID, cwd string) {
	for _, dir := range r.memory.dirs(cwd) {
		if err := r.walkMemoryDir(ctx, id, dir); err != nil {
			r.warn("memory", "walk of %s stopped: %v", dir, err)
			return
		}
	}
}

func (r *hookRunner) walkMemoryDir(ctx context.Context, id uuid.UUID, dir string) (err error) {
	marker := r.state.LoadMarker(dir)
	defer func() {
		if saveErr := r.state.SaveMarker(dir, marker); saveErr != nil && err == nil {
			err = saveErr
		}
	}()
	seen := map[string]bool{}
	unread := false
	for _, path := range memoryFiles(dir) {
		raw, err := os.ReadFile(path)
		if err != nil {
			unread = true
			continue
		}
		m, err := markdown.DecodeMemory(raw)
		if err != nil {
			r.warn("memory", "cannot decode %s: %v", path, err)
			unread = true
			continue
		}
		seen[m.Name] = true
		if e, ok := marker[m.Name]; ok && !m.Modified.After(e.Modified) && e.Scope == entryFor(m).Scope {
			continue
		}
		if err := r.sendMemory(ctx, id, marker, m); err != nil {
			if client.IsCode(err, api.CodeValidation) {
				r.warn("memory", "skipped %s: %v", path, err)
				continue
			}
			return fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
	}
	// A file that could not be read may hold any name, so none counts as gone.
	if unread {
		return nil
	}
	for name, e := range marker {
		if seen[name] {
			continue
		}
		if _, err := r.api.Note(ctx, removedNote(id, name, e, r.state.now())); err != nil {
			if !client.IsCode(err, api.CodeValidation) {
				return fmt.Errorf("%s removed: %w", name, err)
			}
			r.warn("memory", "dropped %s from the marker: %v", name, err)
		}
		delete(marker, name)
	}
	return nil
}
