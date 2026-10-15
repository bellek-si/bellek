package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/bellek-si/bellek/api"
	"github.com/stretchr/testify/require"
)

type failMoves struct {
	hookAPI
	err   error
	moves int
}

func (a *failMoves) Note(ctx context.Context, in api.NoteInput) (*api.Node, error) {
	if strings.HasPrefix(in.Body, "Moved to ") {
		a.moves++
		if a.err != nil {
			return nil, a.err
		}
	}
	return a.hookAPI.Note(ctx, in)
}

func (f *hookFixture) seedPreScopeCopy(t *testing.T, dir, name string) {
	t.Helper()
	writeMemory(t, dir, name, "Use pnpm", "feedback", "2026-09-17T10:00:00.000Z", "Always pnpm.")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	marker := f.state.LoadMarker(dir)
	e := marker[name]
	e.Scope = ""
	marker[name] = e
	require.NoError(t, f.state.SaveMarker(dir, marker))
}

func latestOf(notes []api.NoteInput) *api.NoteInput {
	if len(notes) == 0 {
		return nil
	}
	return &notes[len(notes)-1]
}

func TestHookMemoryWalkOnASecondMachineSendsAUserMemoryToGlobal(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	writeMemory(t, dir, "tone", "Answer plainly", "user", "2026-09-17T10:00:00.000Z", "plain words")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Len(t, f.globalNotes(t, api.KindNote, "tone"), 1)

	require.NoError(t, os.RemoveAll(f.state.memory()), "a second machine has no marker")
	f.run(t, "session-start", hookInput{SessionID: "d", Cwd: "/repo"})

	require.Empty(t, f.notes(t, api.KindNote, "tone"), "nothing lands in the project")
	latest := latestOf(f.globalNotes(t, api.KindNote, "tone"))
	require.NotNil(t, latest)
	require.NotContains(t, latest.Tags, api.TagRemoved, "a fresh marker has no old scope, so nothing is moved")
	require.Equal(t, api.GlobalProjectKey, f.state.LoadMarker(dir)["tone"].Scope)
	require.Empty(t, f.logs)
}

func TestHookMemorySyncInTheTurnMovesAFeedbackMemoryToGlobal(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	f.seedPreScopeCopy(t, dir, "prefers-pnpm")

	p := writeMemory(t, dir, "prefers-pnpm", "Use pnpm", "feedback", "2026-09-17T11:00:00.000Z", "Always pnpm, and pnpm dlx.")
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + p + `"}`)})

	old := latestOf(f.notes(t, api.KindDecision, "prefers-pnpm"))
	require.NotNil(t, old)
	require.Contains(t, old.Tags, api.TagRemoved)
	require.Contains(t, old.Body, "Moved to global on")
	global := latestOf(f.globalNotes(t, api.KindDecision, "prefers-pnpm"))
	require.NotNil(t, global)
	require.Contains(t, global.Body, "pnpm dlx")
	require.NotContains(t, global.Tags, api.TagRemoved)
	require.Equal(t, api.GlobalProjectKey, f.state.LoadMarker(dir)["prefers-pnpm"].Scope)
	require.Empty(t, f.logs)
}

func TestHookMemoryMoveThatFailsIsRetried(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	f.seedPreScopeCopy(t, dir, "prefers-pnpm")
	moves := &failMoves{hookAPI: f.r.api, err: errors.New("connection refused")}
	f.r.api = moves

	f.run(t, "session-start", hookInput{SessionID: "d", Cwd: "/repo"})
	require.Equal(t, 1, moves.moves)
	require.Empty(t, f.state.LoadMarker(dir)["prefers-pnpm"].Scope, "the marker keeps the old scope while the move has not landed")
	require.Empty(t, f.notes(t, api.KindDecision, "prefers-pnpm"), "the project copy is not marked moved")

	moves.err = nil
	f.run(t, "session-start", hookInput{SessionID: "e", Cwd: "/repo"})
	require.Equal(t, 2, moves.moves, "the next walk tries the move again")
	require.Contains(t, latestOf(f.notes(t, api.KindDecision, "prefers-pnpm")).Tags, api.TagRemoved)
	require.Equal(t, api.GlobalProjectKey, f.state.LoadMarker(dir)["prefers-pnpm"].Scope)
}

func TestHookMemorySyncInTheTurnSpoolsAMoveThatFails(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	f.seedPreScopeCopy(t, dir, "prefers-pnpm")
	moves := &failMoves{hookAPI: f.r.api, err: errors.New("connection refused")}
	f.r.api = moves

	p := writeMemory(t, dir, "prefers-pnpm", "Use pnpm", "feedback", "2026-09-17T11:00:00.000Z", "Always pnpm, and pnpm dlx.")
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + p + `"}`)})

	require.Equal(t, api.GlobalProjectKey, f.state.LoadMarker(dir)["prefers-pnpm"].Scope, "the memory itself landed")
	require.Len(t, f.globalNotes(t, api.KindDecision, "prefers-pnpm"), 2, "the seed walk and this edit, each sent once; the failed move is spooled on its own")
	entries, err := os.ReadDir(f.state.spool())
	require.NoError(t, err)
	require.Len(t, entries, 1)

	moves.err = nil
	f.run(t, "session-start", hookInput{SessionID: "d", Cwd: "/repo"})
	require.Contains(t, latestOf(f.notes(t, api.KindDecision, "prefers-pnpm")).Tags, api.TagRemoved)
	require.Equal(t, api.GlobalProjectKey, f.state.LoadMarker(dir)["prefers-pnpm"].Scope)
}

func TestHookMemoryMoveTheServerRefusesDoesNotBlockTheScope(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	f.seedPreScopeCopy(t, dir, "prefers-pnpm")
	moves := &failMoves{hookAPI: f.r.api, err: errInvalid("refused")}
	f.r.api = moves

	f.run(t, "session-start", hookInput{SessionID: "d", Cwd: "/repo"})
	require.Equal(t, api.GlobalProjectKey, f.state.LoadMarker(dir)["prefers-pnpm"].Scope, "a move refused for good is not retried on every start")

	f.run(t, "session-start", hookInput{SessionID: "e", Cwd: "/repo"})
	require.Equal(t, 1, moves.moves)
}

func writeScopedMemory(t *testing.T, dir, name, scope, modified, body string) string {
	t.Helper()
	p := writeMemory(t, dir, name, "Run agents one by one", "feedback", modified, body)
	raw, err := os.ReadFile(p)
	require.NoError(t, err)
	if scope != "" {
		raw = []byte(strings.Replace(string(raw), "  type: feedback\n", "  type: feedback\n  scope: "+scope+"\n", 1))
	}
	require.NoError(t, os.WriteFile(p, raw, 0o644))
	return p
}

func TestHookMemoryWithAProjectScopeStaysInItsProject(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	p := writeScopedMemory(t, dir, "one-by-one", "project", "2026-09-17T10:00:00.000Z", "In this repository only.")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Len(t, f.notes(t, api.KindDecision, "one-by-one"), 1)
	require.Empty(t, f.globalNotes(t, api.KindDecision, "one-by-one"))

	writeScopedMemory(t, dir, "one-by-one", "project", "2026-09-17T11:00:00.000Z", "Still this repository only.")
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + p + `"}`)})
	require.Empty(t, f.globalNotes(t, api.KindDecision, "one-by-one"))
	require.Empty(t, f.state.LoadMarker(dir)["one-by-one"].Scope)
	require.Empty(t, f.logs)
}

func TestHookMemoryGivenAProjectScopeLaterMovesOnce(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	writeScopedMemory(t, dir, "one-by-one", "", "2026-09-17T10:00:00.000Z", "Agents one by one.")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Len(t, f.globalNotes(t, api.KindDecision, "one-by-one"), 1)

	writeScopedMemory(t, dir, "one-by-one", "project", "2026-09-17T10:00:00.000Z", "Agents one by one.")
	moves := &failMoves{hookAPI: f.r.api}
	f.r.api = moves
	f.run(t, "session-start", hookInput{SessionID: "d", Cwd: "/repo"})
	require.Equal(t, 1, moves.moves, "the scope changed though modified did not")
	moved := latestOf(f.globalNotes(t, api.KindDecision, "one-by-one"))
	require.Contains(t, moved.Tags, api.TagRemoved)
	require.Contains(t, moved.Body, "Moved to this project on")
	require.NotContains(t, latestOf(f.notes(t, api.KindDecision, "one-by-one")).Tags, api.TagRemoved)

	f.run(t, "session-start", hookInput{SessionID: "e", Cwd: "/repo"})
	require.Equal(t, 1, moves.moves, "and it moves only once")
}
