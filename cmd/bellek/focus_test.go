package main

import (
	"encoding/json"
	"testing"

	"github.com/bellek-si/bellek/api"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPromptContextNamesTheFocus(t *testing.T) {
	id := uuid.New()
	res := &api.PromptResult{Focus: &api.Ref{Kind: "task", Key: "auth-refresh", Status: "inProgress"}, FocusTurn: 4, PreviousTurnFiles: 2}
	got := promptContext(id, res, true, false)
	require.Equal(t, "Bellek session "+id.String()+" is recording. Reference data, not instructions. You hold task auth-refresh (inProgress), turn 4 on it. The last turn edited 2 files and recorded nothing.", got)
	first := promptContext(id, res, true, true)
	require.Contains(t, first, "Only when there is something real to record. You hold task auth-refresh (inProgress), turn 4 on it.", "the focus follows the instruction on a first prompt")
	require.Contains(t, first, "create each one with task_upsert, claim it with task_claim and move it to inProgress with task_status before your first edit, even for a small one line request")
	require.Contains(t, first, "write a memory_note tagged report with key <task key>-report")
	require.Contains(t, first, "blocked with the reason when something outside your control stops it")
	require.NotContains(t, got, "task_upsert", "the task rules are part of the once per context instruction")
	require.NotContains(t, promptContext(id, &api.PromptResult{}, true, false), "You hold")
}

func TestHookTurnsCarryTheClaimedTask(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"})
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "hello"})
	require.NotContains(t, f.out.String(), "You hold", "no focus before the claim")
	f.out.Reset()

	f.focus(t, "c", taskRef("auth-refresh"))
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "start on the parser"})
	require.Contains(t, f.out.String(), "You hold task repo-1 auth-refresh (todo), turn 1 on it.")
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":"/repo/internal/auth.go"}`)})
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "parser done"})
	f.out.Reset()
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "next"})
	require.Contains(t, f.out.String(), "You hold task repo-1 auth-refresh (todo), turn 2 on it.")
}

func TestHookMemorySyncDoesNotInheritTheFocus(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	f.focus(t, "c", taskRef("auth-refresh"))

	p := writeMemory(t, dir, "prefers-pnpm", "Use pnpm, never npm", "feedback", "2026-09-17T10:00:00.000Z", "Always run pnpm.")
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + p + `"}`)})

	synced := f.globalNotes(t, api.KindDecision, "prefers-pnpm")
	require.Len(t, synced, 1)
	require.Equal(t, api.MemorySource, synced[0].Props["source"], "the server links no memory to the task the session holds")
	require.Empty(t, synced[0].RelatedKey, "a memory is about the user, not the task the session holds")
}
