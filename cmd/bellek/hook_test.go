package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/client"
	"github.com/bellek-si/bellek/internal/config"
	"github.com/bellek-si/bellek/internal/gitinfo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const testRemote = "git@github.com:acme/widgets.git"

type hookFixture struct {
	api    *fakeAPI
	r      *hookRunner
	out    *bytes.Buffer
	logs   []string
	ctx    context.Context
	state  *hookState
	config string

	worktree      string
	worktreeCalls int
	dirty         []string
}

func newHookFixture(t *testing.T) *hookFixture {
	t.Helper()
	f := &hookFixture{api: newFakeAPI(), out: &bytes.Buffer{}, ctx: context.Background(), state: newHookState(t.TempDir()), config: t.TempDir(), worktree: t.TempDir()}
	f.r = &hookRunner{
		memory: memoryLocator{configDir: f.config, toplevel: func(string) string { return "/repo" }},
		api:    f.api, state: f.state, out: f.out,
		cfg: &config.Config{Capture: []string{"prompts", "files", "progress", "memory"}, CaptureIgnore: []string{".env*"}},
		logf: func(event, format string, args ...any) {
			f.logs = append(f.logs, event+": "+fmt.Sprintf(format, args...))
		},
		gitOps: gitOps{
			remote:   func(context.Context, string) string { return testRemote },
			branch:   func(context.Context, string) string { return "main" },
			diffStat: func(context.Context, string) string { return " internal/auth.go | 2 +-" },
			worktrees: func(_ context.Context, cwd string) []string {
				f.worktreeCalls++
				return []string{cwd, f.worktree}
			},
			status: func(context.Context, string) []string { return append([]string{}, f.dirty...) },
		},
	}
	return f
}

func (f *hookFixture) run(t *testing.T, event string, in hookInput) {
	t.Helper()
	require.NoError(t, f.r.run(f.ctx, event, in))
}

func (f *hookFixture) session(t *testing.T, claudeID string) *fakeSession {
	t.Helper()
	id, ok := f.state.Load(claudeID)
	require.True(t, ok)
	s := f.api.byID(id)
	require.NotNil(t, s, "the hooks mapped %s to a session the server never started", claudeID)
	return s
}

func (f *hookFixture) checkpoints() []api.NoteInput {
	var out []api.NoteInput
	for _, n := range f.api.noteLog {
		if slices.Contains(n.Tags, "checkpoint") {
			out = append(out, n)
		}
	}
	return out
}

func TestHookLifecycle(t *testing.T) {
	f := newHookFixture(t)
	host, _ := os.Hostname()
	in := hookInput{SessionID: "claude-1", Cwd: "/repo", Source: "startup"}
	f.run(t, "session-start", in)
	s := f.session(t, "claude-1")
	require.Len(t, f.api.sessions, 1)
	require.Equal(t, api.SessionStartInput{Cwd: "/repo", RepoRemote: testRemote, RepoRoot: "/repo", AgentKind: "claudeCode", AgentID: host, Branch: "main"}, s.Start)

	f.run(t, "pre-compact", hookInput{SessionID: "claude-1", Cwd: "/repo", Trigger: "auto"})
	checkpoints := f.checkpoints()
	require.Len(t, checkpoints, 1)
	require.Equal(t, s.ID, checkpoints[0].SessionID)
	require.Equal(t, "Checkpoint before compaction", checkpoints[0].Title)
	require.Contains(t, checkpoints[0].Body, "Context compaction (auto)")
	require.Contains(t, checkpoints[0].Body, "Branch: main")
	require.Contains(t, checkpoints[0].Body, "internal/auth.go | 2 +-")

	f.run(t, "session-end", hookInput{SessionID: "claude-1", Cwd: "/repo", Reason: "other"})
	_, ok := f.state.Load("claude-1")
	require.False(t, ok)
	require.Equal(t, "session ended by claude code: other", s.EndReason)
	require.Len(t, s.Progress, 1)
	require.True(t, strings.HasPrefix(s.Progress[0].Message, "git diff --stat\n"))
	require.Empty(t, f.out.String(), "only the prompt hook prints")
}

func TestHookCapturesATurn(t *testing.T) {
	f := newHookFixture(t)
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "main.go"), []byte("package main"), 0o644))
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: cwd})

	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: cwd, Prompt: "fix main.go, token=sk-ant-api03-abcdefghijklmnopqrstuvwxyz"})
	var answer map[string]map[string]string
	require.NoError(t, json.Unmarshal(f.out.Bytes(), &answer))
	ctxLine := answer["hookSpecificOutput"]["additionalContext"]
	require.Equal(t, "UserPromptSubmit", answer["hookSpecificOutput"]["hookEventName"])
	s := f.session(t, "c")
	require.Contains(t, ctxLine, "Bellek session "+s.ID.String()+" is recording")
	require.Contains(t, ctxLine, "memory_note")
	require.NotContains(t, ctxLine, "recorded nothing")
	require.Equal(t, []string{"fix main.go, token=[redacted:env]"}, s.Prompts)

	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: cwd, ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":"` + cwd + `/main.go"}`)})
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: cwd, ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + cwd + `/.env"}`)})
	bash := hookInput{SessionID: "c", Cwd: cwd, ToolName: "Bash", ToolUseID: "toolu_1", ToolInput: json.RawMessage(`{"command":"go build ./... && cat main.go"}`)}
	f.run(t, "pre-tool-use", bash)
	f.run(t, "tool-use", bash)
	require.Equal(t, [][]string{{"main.go"}}, s.Artifacts, "the ignored file and a bash call that changed nothing send no path")

	f.run(t, "stop", hookInput{SessionID: "c", Cwd: cwd, LastAssistantMessage: "Done. <private>secret plan</private> Built main.go."})
	require.Equal(t, []api.ProgressInput{{Message: "Done.  Built main.go."}}, s.Progress)

	f.out.Reset()
	f.api.prompt = api.PromptResult{PreviousTurnFiles: 1}
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: cwd, Prompt: "and the tests"})
	require.NoError(t, json.Unmarshal(f.out.Bytes(), &answer))
	require.Contains(t, answer["hookSpecificOutput"]["additionalContext"], "The last turn edited 1 file and recorded nothing.")
	require.Len(t, s.Prompts, 2)
}

func TestHookStopIgnoresEmptyMessagesAndNeverPrints(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "  "})
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Read", ToolInput: json.RawMessage(`{"file_path":"/repo/a.go"}`)})
	require.Empty(t, f.session(t, "c").Progress)
	require.Empty(t, f.session(t, "c").Artifacts)
	require.Empty(t, f.out.String())
}

func TestHookPreCompactLeavesOutTheDiffWhenThereIsNone(t *testing.T) {
	f := newHookFixture(t)
	f.r.gitOps.diffStat = func(context.Context, string) string { return "" }
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	f.run(t, "pre-compact", hookInput{SessionID: "c", Cwd: "/repo", Trigger: "manual"})
	checkpoints := f.checkpoints()
	require.Len(t, checkpoints, 1)
	body := checkpoints[0].Body
	require.Contains(t, body, "Context compaction (manual)")
	require.Contains(t, body, "Branch: main")
	require.NotContains(t, body, "```")
}

func TestHookPromptRecordsNothingWhenRedactionEmptiesIt(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "<private>the plan</private>"})
	require.Zero(t, f.api.called("Prompt"))
	require.Contains(t, f.out.String(), "is recording", "the context line still names the session")
}

func TestHookCaptureStreamsCanBeTurnedOff(t *testing.T) {
	f := newHookFixture(t)
	f.r.cfg.Capture = nil
	cwd := t.TempDir()
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: cwd})
	s := f.session(t, "c")
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: cwd, Prompt: "hello"})
	require.Contains(t, f.out.String(), "is recording", "the context line still names the session")
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: cwd, ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + cwd + `/x.go"}`)})
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: cwd, LastAssistantMessage: "done"})
	f.r.gitOps.branch = func(context.Context, string) string { return "feat/x" }
	f.run(t, "cwd", hookInput{SessionID: "c", Cwd: cwd + "/sub"})
	require.Len(t, s.Locations, 1)
	require.Equal(t, "feat/x", s.Locations[0].Branch, "the location hook keeps working with capture off")
	f.run(t, "session-end", hookInput{SessionID: "c", Cwd: cwd})
	require.True(t, s.ended)
	require.Empty(t, s.Prompts)
	require.Empty(t, s.Artifacts)
	require.Empty(t, s.Progress)
	require.Empty(t, s.Notes)
}

func TestHookSessionStartHonoursSource(t *testing.T) {
	f := newHookFixture(t)
	in := hookInput{SessionID: "claude-2", Cwd: "/repo"}
	f.run(t, "session-start", in)
	first := f.session(t, "claude-2").ID
	for _, source := range []string{"startup", "resume", "compact", "fork"} {
		in.Source = source
		f.run(t, "session-start", in)
		require.Equal(t, first, f.session(t, "claude-2").ID, "a second start with source %s reuses the mapped session", source)
	}
	require.Len(t, f.api.sessions, 1)
	in.Source = "clear"
	f.run(t, "session-start", in)
	require.NotEqual(t, first, f.session(t, "claude-2").ID, "clear starts a new session")
	require.Equal(t, "context cleared by claude code", f.api.byID(first).EndReason, "and ends the one it replaces")

	f.run(t, "session-start", hookInput{SessionID: "claude-3", Cwd: "/repo", Source: "fork"})
	require.NotEqual(t, f.session(t, "claude-2").ID, f.session(t, "claude-3").ID, "a fork carries a new id and starts its own session")
	require.Equal(t, 1, f.api.called("SessionEndReason"), "a fork never ends a session")

	f.run(t, "session-end", hookInput{SessionID: "unknown", Cwd: "/repo"})
	f.run(t, "pre-compact", hookInput{SessionID: "unknown", Cwd: "/repo"})
	f.run(t, "prompt", hookInput{SessionID: "unknown", Cwd: "/repo", Prompt: "x"})
	require.Empty(t, f.out.String(), "an unmapped session gets no context line")
	require.Error(t, f.r.run(f.ctx, "dance", in))
	require.Error(t, f.r.run(f.ctx, "session-start", hookInput{}))
}

func TestHookSpoolsWhenTheServerIsDownAndDrainsLater(t *testing.T) {
	f := newHookFixture(t)
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "a.go"), []byte("x"), 0o644))
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: cwd})

	f.api.down = true
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: cwd, Prompt: "first"})
	require.Contains(t, f.out.String(), "is recording", "the context line is printed even when the server is down")
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: cwd, ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":"` + cwd + `/a.go"}`)})
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: cwd, LastAssistantMessage: "did it"})
	entries, err := os.ReadDir(f.state.spool())
	require.NoError(t, err)
	require.Len(t, entries, 3)
	require.Empty(t, f.api.sent)

	f.api.down = false
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: cwd, Prompt: "second"})
	require.Equal(t, []string{"prompt:first", "artifacts:a.go", "progress:did it", "prompt:second"}, f.api.sent, "spooled requests go first, in order")
	entries, _ = os.ReadDir(f.state.spool())
	require.Empty(t, entries)
	require.Equal(t, []string{"first", "second"}, f.session(t, "c").Prompts)
}

func TestHookDropsASpooledCaptureTheServerRefusesForGood(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	f.api.down = true
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "one"})
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "two"})
	entries, err := os.ReadDir(f.state.spool())
	require.NoError(t, err)
	require.Len(t, entries, 2)

	id, _ := f.state.Load("c")
	f.api.end(id)
	f.api.down = false
	f.r.drain(f.ctx)
	entries, _ = os.ReadDir(f.state.spool())
	require.Empty(t, entries, "a sessionEnded answer would come again, so neither item waits in the spool")
	require.Equal(t, []string{"progress:one", "progress:two"}, f.api.sent, "each item was tried once")
	require.Contains(t, strings.Join(f.logs, "\n"), "spooled capture dropped")
}

func TestHookDropsSpooledCapturesOnceTheKeyIsRefused(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	f.api.down = true
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "one"})
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "two"})

	f.api.down = false
	f.api.errs["Progress"] = errRefusedKey
	f.r.drain(f.ctx)
	entries, _ := os.ReadDir(f.state.spool())
	require.Empty(t, entries, "a capture never waits for a new key, which may belong to another workspace")
}

func TestHookDoesNotSpoolRejectedCaptures(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	id, _ := f.state.Load("c")
	f.api.end(id)
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "late"})
	_, err := os.ReadDir(f.state.spool())
	require.True(t, os.IsNotExist(err), "a sessionEnded rejection is not retried")
	require.NotEmpty(t, f.logs)
}

func TestHookPromptStartsAgainWhenTheServerEndedTheSession(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	old, _ := f.state.Load("c")
	f.api.end(old)

	f.out.Reset()
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "keep going"})
	fresh, ok := f.state.Load("c")
	require.True(t, ok)
	require.NotEqual(t, old, fresh, "the ended session is replaced")
	require.Contains(t, f.out.String(), fresh.String(), "the answer names the session that records")
	require.NotContains(t, f.out.String(), old.String())
	require.Contains(t, f.out.String(), "task_upsert", "a new session gets the full instruction")
	require.Equal(t, []string{"keep going"}, f.session(t, "c").Prompts, "the prompt lands on the new session")
}

func TestHookPromptStartsAgainWhenTheProjectWasDeleted(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	old, _ := f.state.Load("c")
	f.api.forget(old)

	f.out.Reset()
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "keep going"})
	fresh, ok := f.state.Load("c")
	require.True(t, ok)
	require.NotEqual(t, old, fresh, "the deleted session is replaced")
	require.Contains(t, f.out.String(), fresh.String())
	require.Equal(t, []string{"keep going"}, f.session(t, "c").Prompts, "the prompt lands on the new session")
}

func TestHookSessionStartReplacesASessionTheServerEnded(t *testing.T) {
	f := newHookFixture(t)
	f.r.cfg.Recall = []string{"briefing"}
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"})
	old, _ := f.state.Load("c")
	f.api.end(old)

	f.out.Reset()
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "resume"})
	fresh, _ := f.state.Load("c")
	require.NotEqual(t, old, fresh)
	require.Contains(t, f.out.String(), fresh.String())
}

func TestHookSessionStartReplacesASessionWhoseProjectWasDeleted(t *testing.T) {
	f := newHookFixture(t)
	f.r.cfg.Recall = []string{"briefing"}
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"})
	old, _ := f.state.Load("c")
	f.api.forget(old)

	f.out.Reset()
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "resume"})
	fresh, _ := f.state.Load("c")
	require.NotEqual(t, old, fresh)
	require.Contains(t, f.out.String(), fresh.String())
	require.Empty(t, f.logs, "the restart is silent")
}

func TestHookSessionStartDoesNotReplaceAnEndedSessionOutsideARepository(t *testing.T) {
	f := newHookFixture(t)
	f.r.cfg.Recall = []string{"briefing"}
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"})
	old, _ := f.state.Load("c")
	f.api.end(old)
	f.r.gitOps.remote = func(context.Context, string) string { return "" }
	f.r.gitOps.worktrees = func(context.Context, string) []string { return nil }

	f.out.Reset()
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/plain", Source: "resume"})
	_, ok := f.state.Load("c")
	require.False(t, ok, "the ended session is unmapped and no new one is started")
	require.Empty(t, f.out.String(), "the model is told nothing")
	require.Empty(t, f.logs, "skipping is not a failure")
	require.Len(t, f.api.sessions, 1)
}

func TestHookPromptDoesNotReplaceAnEndedSessionOutsideARepository(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	old, _ := f.state.Load("c")
	f.api.end(old)
	f.r.gitOps.remote = func(context.Context, string) string { return "" }
	f.r.gitOps.worktrees = func(context.Context, string) []string { return nil }

	f.out.Reset()
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/plain", Prompt: "keep going"})
	_, ok := f.state.Load("c")
	require.False(t, ok, "the ended session is unmapped and no new one is started")
	require.Empty(t, f.out.String(), "the model is told nothing")
	require.Empty(t, f.logs, "skipping is not a failure")
	require.Len(t, f.api.sessions, 1)
}

func TestSessionGoneOnlyForAnEndedOrUnknownSession(t *testing.T) {
	for _, tt := range []struct {
		err  error
		gone bool
	}{
		{&client.Error{Code: "sessionEnded", Message: "session has ended"}, true},
		{&client.Error{Code: "notFound", Message: "session not found"}, true},
		{fmt.Errorf("prompt: %w", &client.Error{Code: "notFound", Message: "session not found"}), true},
		{&client.Error{Code: "notFound", Message: "task t1 not found"}, false},
		{&client.Error{Code: "validation", Message: "session not found"}, false},
		{errors.New("session not found"), false},
		{nil, false},
	} {
		require.Equal(t, tt.gone, sessionGone(tt.err), "%v", tt.err)
	}
}

func TestHookPromptInstructsAfterAFileLineInANewContext(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"})
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "first"})
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "compact"})
	require.NoError(t, f.state.SaveRecalled("c", recallMarker{Files: []string{"internal/auth.go"}}), "the file line of an edit before the next prompt")

	f.out.Reset()
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "after the compaction"})
	require.Contains(t, f.out.String(), "task_upsert", "the compacted context lost the instruction, so it comes back")

	f.out.Reset()
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "and again"})
	require.NotContains(t, f.out.String(), "task_upsert")
	require.Equal(t, []string{"internal/auth.go"}, marker(f.state, "c").Files, "the file stays recalled")
}

func TestHookPromptStaysQuietWhenTheEndedSessionCannotBeReplaced(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	old, _ := f.state.Load("c")
	f.api.end(old)
	f.api.errs["SessionStart"] = errUnreachable

	f.out.Reset()
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "keep going"})
	require.Empty(t, f.out.String(), "no context line names a session that does not record")
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "session ended and none started")
	_, ok := f.state.Load("c")
	require.False(t, ok, "the ended session is forgotten so no later hook writes to it")
	_, err := os.ReadDir(f.state.spool())
	require.True(t, os.IsNotExist(err), "a prompt for an ended session is not spooled")
}

func TestHookSessionStartFailsWhenTheEndedSessionCannotBeReplaced(t *testing.T) {
	f := newHookFixture(t)
	f.r.cfg.Recall = []string{"briefing"}
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"})
	old, _ := f.state.Load("c")
	f.api.end(old)
	f.api.errs["SessionStart"] = errUnreachable

	f.out.Reset()
	require.Error(t, f.r.run(f.ctx, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "resume"}))
	require.Empty(t, f.out.String())
	_, ok := f.state.Load("c")
	require.False(t, ok)
}

func TestHookSessionStartSaysWhenTheServerIsDown(t *testing.T) {
	f := newHookFixture(t)
	f.r.cfg.Recall = []string{"briefing"}
	f.r.cfg.ServerURL = "http://bellek.test:8080"
	f.api.errs["SessionStart"] = errUnreachable

	require.Error(t, f.r.run(f.ctx, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"}))
	require.Contains(t, f.out.String(), "Bellek is not reachable at http://bellek.test:8080, so this session is not recorded.")

	f.out.Reset()
	f.r.cfg.Recall = nil
	require.Error(t, f.r.run(f.ctx, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"}))
	require.Empty(t, f.out.String(), "BELLEK_RECALL= keeps the hook silent")
}

func TestHookSessionStartSaysWhenTheServerIsTooSlow(t *testing.T) {
	f := newHookFixture(t)
	f.r.cfg.Recall = []string{"briefing"}
	f.r.cfg.ServerURL = "http://bellek.test:8080"
	f.api.errs["SessionStart"] = &url.Error{Op: "Post", URL: "http://bellek.test/v1/sessions", Err: context.DeadlineExceeded}

	require.Error(t, f.r.run(f.ctx, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"}))
	require.Contains(t, f.out.String(), "Bellek at http://bellek.test:8080 did not answer in time, so this session is not recorded.")
	require.NotContains(t, f.out.String(), "not reachable", "a slow server is not a missing one")

	f.out.Reset()
	f.r.cfg.Recall = nil
	require.Error(t, f.r.run(f.ctx, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"}))
	require.Empty(t, f.out.String(), "BELLEK_RECALL= keeps the hook silent")
}

func TestHookSessionStartSaysWhenTheKeyIsRefused(t *testing.T) {
	f := newHookFixture(t)
	f.r.cfg.Recall = []string{"briefing"}
	f.r.cfg.ServerURL = "http://bellek.test:8080"
	f.api.errs["SessionStart"] = errRefusedKey

	require.Error(t, f.r.run(f.ctx, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"}))
	require.Contains(t, f.out.String(), "Bellek at http://bellek.test:8080 refused BELLEK_TOKEN, so this session is not recorded.")

	f.out.Reset()
	f.r.cfg.Recall = nil
	require.Error(t, f.r.run(f.ctx, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"}))
	require.Empty(t, f.out.String(), "BELLEK_RECALL= keeps the hook silent")
}

func TestHookPromptSaysOnceWhenTheKeyIsRevokedMidSession(t *testing.T) {
	f := newHookFixture(t)
	f.r.cfg.ServerURL = "http://bellek.test:8080"
	f.r.cfg.Recall = []string{"prompt"}
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	f.api.errs["Prompt"] = errRefusedKey

	f.out.Reset()
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "keep going"})
	require.Contains(t, f.out.String(), "Bellek at http://bellek.test:8080 refused BELLEK_TOKEN, so this session is not recorded.")
	require.NotContains(t, f.out.String(), "is recording")
	_, err := os.ReadDir(f.state.spool())
	require.True(t, os.IsNotExist(err), "a refused capture is not spooled")
	_, ok := f.state.Load("c")
	require.False(t, ok)

	f.out.Reset()
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "and again"})
	require.Empty(t, f.out.String(), "later prompts stay quiet")
	require.Equal(t, 1, f.api.called("Prompt"), "and send nothing")
}

func TestHookPromptStaysQuietOnARefusedKeyWithoutPromptRecall(t *testing.T) {
	f := newHookFixture(t)
	f.r.cfg.Recall = nil
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	f.api.errs["Prompt"] = errRefusedKey

	f.out.Reset()
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "keep going"})
	require.Empty(t, f.out.String(), "BELLEK_RECALL= keeps the hook silent")
	_, ok := f.state.Load("c")
	require.False(t, ok, "the session stops recording all the same")
	_, err := os.ReadDir(f.state.spool())
	require.True(t, os.IsNotExist(err), "a refused capture is not spooled")
	require.Contains(t, strings.Join(f.logs, "\n"), "prompt: capture rejected")
}

func TestHookSessionStartStaysQuietWhenTheServerAnswers(t *testing.T) {
	for _, err := range []*client.Error{
		{Status: 400, Code: "validation", Message: "cwd is required"},
		{Status: 500, Code: "internal", Message: "boom"},
	} {
		t.Run(err.Code, func(t *testing.T) {
			f := newHookFixture(t)
			f.r.cfg.Recall = []string{"briefing"}
			f.api.errs["SessionStart"] = err

			require.Error(t, f.r.run(f.ctx, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"}))
			require.Empty(t, f.out.String(), "a server that answered is reachable")
		})
	}
}

func TestHookSessionStartStaysQuietWhenOnlyTheLocalStateFails(t *testing.T) {
	f := newHookFixture(t)
	f.r.cfg.Recall = []string{"briefing"}

	require.Error(t, f.r.run(f.ctx, "session-start", hookInput{SessionID: "a/b", Cwd: "/repo", Source: "startup"}))
	require.Len(t, f.api.sessions, 1)
	require.NotContains(t, f.out.String(), "not reachable", "the server answered and recorded the session")
}

func TestHookPromptDoesNotAckTheEndedSessionsSignalsOnTheNewOne(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	old, _ := f.state.Load("c")
	sig := uuid.New()
	require.NoError(t, f.state.SaveRecalled("c", recallMarker{Instructed: true, Signals: []uuid.UUID{sig}}))
	f.api.end(old)

	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "keep going"})
	require.Equal(t, [][]uuid.UUID{{sig}, nil}, f.api.promptAcks, "the old signals belong to the ended session")
	require.True(t, marker(f.state, "c").Instructed)
}

func TestSpoolDropsOldEntriesAndStopsAtTheFirstFailure(t *testing.T) {
	state := newHookState(t.TempDir())
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now.Add(-8 * 24 * time.Hour) }
	require.NoError(t, state.Spool(spooled{Op: "progress", Message: "old"}))
	state.now = func() time.Time { return now }
	require.NoError(t, state.Spool(spooled{Op: "progress", Message: "one"}))
	require.NoError(t, state.Spool(spooled{Op: "progress", Message: "two"}))
	require.NoError(t, state.Spool(spooled{Op: "progress", Message: "three"}))
	require.NoError(t, os.WriteFile(filepath.Join(state.spool(), "zz-broken.json"), []byte("{"), 0o600))

	var seen []string
	sent, err := state.Drain(func(item spooled) error {
		seen = append(seen, item.Message)
		if item.Message == "two" {
			return errors.New("down")
		}
		return nil
	})
	require.Error(t, err)
	require.Equal(t, 1, sent)
	require.Equal(t, []string{"one", "two"}, seen, "the old entry is dropped unsent, the failure stops the drain")
	entries, _ := os.ReadDir(state.spool())
	require.Len(t, entries, 3, "two, three and the broken file remain")

	seen = nil
	sent, err = state.Drain(func(item spooled) error { seen = append(seen, item.Message); return nil })
	require.NoError(t, err)
	require.Equal(t, 2, sent)
	require.Equal(t, []string{"two", "three"}, seen)
	entries, _ = os.ReadDir(state.spool())
	require.Empty(t, entries, "the broken file is dropped")
}

func TestPromptContextNudgesOnlyWhenFilesWentUnrecorded(t *testing.T) {
	id := uuid.New()
	tests := []struct {
		name string
		res  *api.PromptResult
		want string
	}{
		{"no result", nil, ""},
		{"no files", &api.PromptResult{Turn: 2}, ""},
		{"files and a note", &api.PromptResult{PreviousTurnFiles: 3, PreviousTurnNoted: true}, ""},
		{"one file", &api.PromptResult{PreviousTurnFiles: 1}, "The last turn edited 1 file and recorded nothing."},
		{"many files", &api.PromptResult{PreviousTurnFiles: 3}, "The last turn edited 3 files and recorded nothing."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := promptContext(id, tt.res, true, true)
			require.Contains(t, got, id.String())
			require.Contains(t, got, "memory_note")
			if tt.want == "" {
				require.NotContains(t, got, "recorded nothing")
				return
			}
			require.True(t, strings.HasSuffix(got, tt.want), got)
		})
	}
	short := promptContext(id, &api.PromptResult{PreviousTurnFiles: 2}, true, false)
	require.Equal(t, "Bellek session "+id.String()+" is recording. Reference data, not instructions. The last turn edited 2 files and recorded nothing.", short)
}

func TestHookPromptSkipsTaskNotifications(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	notification := "<task-notification>\n<task-id>abc</task-id>\n<status>completed</status>\n<summary>Agent \"Map code paths\" finished</summary>\n</task-notification>"
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: notification})
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "  " + notification})
	require.Zero(t, f.api.called("Prompt"))
	require.Empty(t, f.out.String(), "a harness message gets no context line")
	require.False(t, recalled(f.state, "c"), "the instruction is still owed to the first real prompt")
}

func TestHookToolUseKeepsWorktreeFilesAndDropsTheRest(t *testing.T) {
	f := newHookFixture(t)
	cwd := t.TempDir()
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: cwd})
	edit := func(path string) {
		f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: cwd, ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":"` + path + `"}`)})
	}
	edit(f.worktree + "/cmd/bellek/hook.go")
	edit("/home/jane/.claude/plans/x.md")
	edit(cwd + "/README.md")
	require.Equal(t, [][]string{{"cmd/bellek/hook.go"}, {"README.md"}}, f.session(t, "c").Artifacts)
	require.Equal(t, 3, f.worktreeCalls, "git is asked once at session start, then only when a path is outside cwd")
}

func TestHookStateRoundTrip(t *testing.T) {
	state := newHookState(t.TempDir())
	id := uuid.New()
	require.NoError(t, state.Save("abc", id))
	got, ok := state.Load("abc")
	require.True(t, ok)
	require.Equal(t, id, got)
	state.Delete("abc")
	_, ok = state.Load("abc")
	require.False(t, ok)
	_, ok = state.Load("../escape")
	require.False(t, ok)
	require.Error(t, state.Save("a/b", id))
}

func TestHookBudget(t *testing.T) {
	require.Equal(t, 4*time.Second, hookBudget("session-start"), "session start pays a TLS handshake and stays under the 5s hooks.json limit")
	for _, event := range hookEvents[1:] {
		require.Equal(t, time.Second, hookBudget(event), event)
	}
}

func TestHookStateSaveCreatesTheStateDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "a", "b")
	state := newHookState(root)
	id := uuid.New()
	require.NoError(t, state.Save("claude-1", id))
	require.DirExists(t, filepath.Join(root, "sessions"))
	got, ok := state.Load("claude-1")
	require.True(t, ok)
	require.Equal(t, id, got)

	info, err := os.Stat(filepath.Join(root, "sessions"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "session ids stay private to the user")
}

func TestDefaultHookStateDirectory(t *testing.T) {
	stateDir := t.TempDir()
	xdgDir := t.TempDir()
	homeDir := t.TempDir()
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"explicit state dir wins", map[string]string{"BELLEK_STATE_DIR": stateDir, "XDG_STATE_HOME": xdgDir}, stateDir},
		{"xdg state home", map[string]string{"XDG_STATE_HOME": xdgDir}, filepath.Join(xdgDir, "bellek")},
		{"home fallback", map[string]string{"HOME": homeDir}, filepath.Join(homeDir, ".local", "state", "bellek")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BELLEK_STATE_DIR", "")
			t.Setenv("XDG_STATE_HOME", "")
			t.Setenv("HOME", homeDir)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			require.Equal(t, tt.want, defaultHookState().root)
			require.Equal(t, filepath.Join(tt.want, "sessions"), defaultHookState().sessions())
			require.Equal(t, filepath.Join(tt.want, "spool"), defaultHookState().spool())
		})
	}
}

func TestHookCommandStaysQuietWhenTheServerIsUnreachable(t *testing.T) {
	repo := t.TempDir()
	out, err := exec.Command("git", "-C", repo, "init", "--quiet").CombinedOutput()
	require.NoError(t, err, string(out))
	dirs := strings.NewReplacer("REPO", repo, "PLAIN", t.TempDir())
	tests := []struct {
		name  string
		event string
		stdin string
		want  string
	}{
		{"garbage", "session-start", "not json at all", ""},
		{"empty", "session-start", "", ""},
		{"valid json", "session-start", `{"session_id":"claude-1","cwd":"REPO","hook_event_name":"SessionStart"}`, "Bellek is not reachable at http://127.0.0.1:1"},
		{"outside a repository", "session-start", `{"session_id":"claude-2","cwd":"PLAIN","hook_event_name":"SessionStart"}`, ""},
		{"stop without a session", "stop", `{"session_id":"claude-9","last_assistant_message":"hi"}`, ""},
		{"prompt without a session", "prompt", `{"session_id":"claude-9","prompt":"hi"}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setConfigEnv(t)
			t.Setenv("BELLEK_URL", "http://127.0.0.1:1")
			t.Setenv("BELLEK_STATE_DIR", t.TempDir())
			out, err := runWithStdin(t, dirs.Replace(tt.stdin), "hook", tt.event)
			require.NoError(t, err, "a hook must never fail the agent it runs inside")
			if tt.want == "" {
				require.Empty(t, out)
				return
			}
			require.Contains(t, out, tt.want, "a new session tells the model it is not recorded, and nothing else")
			require.Equal(t, 1, strings.Count(out, "\n"))
		})
	}
}

func TestHookCommandRejectsAnUnknownEvent(t *testing.T) {
	_, err := runWithStdin(t, "", "hook", "session-start", "session-end")
	require.Error(t, err, "the hook takes exactly one event")
	_, err = runWithStdin(t, "", "hook", "dance")
	require.Error(t, err)
}

const memoryFile = `---
name: %s
description: "%s"
metadata:
  node_type: memory
  type: %s
  originSessionId: 11111111-2222-4333-8444-555555555555
  modified: %s
---

%s
`

func (f *hookFixture) memoryDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(f.config, "projects", claudeProjectName("/repo"), "memory")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return dir
}

func writeMemory(t *testing.T, dir, name, description, memoryType, modified, body string) string {
	t.Helper()
	p := filepath.Join(dir, name+".md")
	require.NoError(t, os.WriteFile(p, []byte(fmt.Sprintf(memoryFile, name, description, memoryType, modified, body)), 0o644))
	return p
}

func (f *hookFixture) notes(t *testing.T, kind api.Kind, key string) []api.NoteInput {
	t.Helper()
	return f.api.notesIn("", kind, key)
}

func (f *hookFixture) globalNotes(t *testing.T, kind api.Kind, key string) []api.NoteInput {
	t.Helper()
	return f.api.notesIn(api.GlobalProjectKey, kind, key)
}

func TestHookSyncsAMemoryWriteInTheTurn(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	s := f.session(t, "c")

	p := writeMemory(t, dir, "prefers-pnpm", "Use pnpm, never npm", "feedback", "2026-09-17T10:00:00.000Z", "Always run pnpm. token=sk-ant-api03-abcdefghijklmnopqrstuvwxyz")
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + p + `","content":"x"}`)})
	decisions := f.globalNotes(t, api.KindDecision, "prefers-pnpm")
	require.Len(t, decisions, 1)
	d := decisions[0]
	require.Equal(t, s.ID, d.SessionID)
	require.Equal(t, "Use pnpm, never npm", d.Title)
	require.Equal(t, "Always run pnpm. token=[redacted:env]", d.Body)
	require.Equal(t, []string{"memory", "feedback"}, d.Tags)
	require.Equal(t, "claudeMemory", d.Props["source"])
	require.Equal(t, "feedback", d.Props["memoryType"])
	require.Equal(t, "2026-09-17T10:00:00Z", d.Props["modified"])
	require.Equal(t, "11111111-2222-4333-8444-555555555555", d.Props["originSessionId"])
	require.Empty(t, s.Artifacts, "a memory file is not an artifact")

	writeMemory(t, dir, "prefers-pnpm", "Use pnpm, never npm", "feedback", "2026-09-17T11:00:00.000Z", "Always run pnpm, and pnpm dlx.")
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":"` + p + `","old_string":"a","new_string":"b"}`)})
	versions := f.globalNotes(t, api.KindDecision, "prefers-pnpm")
	require.Len(t, versions, 2, "an edit sends the memory again")
	require.Equal(t, "Always run pnpm, and pnpm dlx.", versions[1].Body)

	proj := writeMemory(t, dir, "repo-layout", "Where things live", "project", "2026-09-17T10:00:00.000Z", "Widgets live in internal/widgets.")
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + proj + `"}`)})
	require.Len(t, f.notes(t, api.KindNote, "repo-layout"), 1, "a project memory is a note")

	index := filepath.Join(dir, "MEMORY.md")
	require.NoError(t, os.WriteFile(index, []byte("- [x](x.md)\n"), 0o644))
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + index + `"}`)})
	require.Len(t, s.Notes, 3, "the index is skipped")
	require.Empty(t, f.out.String())
}

func TestHookRedactsTheTitleOfASyncedMemory(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})

	p := writeMemory(t, dir, "blob-store", "Use AccountKey=abcdefghijklmnopqrs for the blob store", "project", "2026-09-17T10:00:00.000Z", "nothing here")
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + p + `","content":"x"}`)})
	notes := f.notes(t, api.KindNote, "blob-store")
	require.Len(t, notes, 1)
	require.Equal(t, "Use AccountKey=[redacted:env] for the blob store", notes[0].Title)
	require.Equal(t, "blob-store", notes[0].Key, "the key stays the raw name so the marker still matches")
}

func TestHookMemoryStreamCanBeTurnedOff(t *testing.T) {
	f := newHookFixture(t)
	f.r.cfg.Capture = []string{"files"}
	dir := f.memoryDir(t)
	p := writeMemory(t, dir, "fact", "A fact", "project", "2026-09-17T10:00:00.000Z", "body")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + p + `"}`)})
	s := f.session(t, "c")
	require.Empty(t, s.Notes)
	require.Empty(t, s.Artifacts, "a memory file never becomes an artifact")
}

func TestHookSessionStartWalksMemoryAndNoticesRemovals(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	writeMemory(t, dir, "one", "One", "feedback", "2026-09-17T10:00:00.000Z", "1")
	writeMemory(t, dir, "two", "Two", "project", "2026-09-17T10:00:00.000Z", "2")
	three := writeMemory(t, dir, "three", "Three", "reference", "2026-09-17T10:00:00.000Z", "3")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("index"), 0o644))

	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Len(t, f.session(t, "c").Notes, 3, "the index is not a memory")
	require.Len(t, f.notes(t, api.KindNote, "two"), 1)
	require.Len(t, f.notes(t, api.KindNote, "three"), 1)
	require.Len(t, f.globalNotes(t, api.KindDecision, "one"), 1)

	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "resume"})
	require.Len(t, f.globalNotes(t, api.KindDecision, "one"), 1, "unchanged files are not sent again")

	writeMemory(t, dir, "two", "Two again", "project", "2026-09-17T12:00:00.000Z", "2b")
	require.NoError(t, os.Remove(three))
	f.run(t, "session-start", hookInput{SessionID: "d", Cwd: "/repo"})
	two := f.notes(t, api.KindNote, "two")
	require.Len(t, two, 2, "a newer file is sent again")
	require.Equal(t, "2b", two[1].Body)
	removed := f.notes(t, api.KindNote, "three")
	require.Len(t, removed, 2)
	gone := removed[1]
	require.Contains(t, gone.Tags, "removed")
	require.Contains(t, gone.Body, "Removed from Claude Code memory on")
	require.Equal(t, "Three", gone.Title)
	marker := f.state.LoadMarker(dir)
	require.NotContains(t, marker, "three")
	require.Contains(t, marker, "two")

	f.run(t, "session-start", hookInput{SessionID: "e", Cwd: "/repo"})
	require.Len(t, f.notes(t, api.KindNote, "three"), 2, "a removal is sent once")
}

func TestHookMemoryWalkStopsAtTheFirstFailureAndResumes(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	writeMemory(t, dir, "a", "A", "project", "2026-09-17T10:00:00.000Z", "a")
	writeMemory(t, dir, "b", "B", "project", "2026-09-17T10:00:00.000Z", "b")
	f.api.failNotes = 1
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Len(t, f.state.LoadMarker(dir), 0, "nothing was sent")
	require.Empty(t, f.api.sent, "the walk stopped at the first failure")

	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "resume"})
	require.Len(t, f.state.LoadMarker(dir), 2)
	require.Len(t, f.notes(t, api.KindNote, "a"), 1)
	require.Len(t, f.notes(t, api.KindNote, "b"), 1)
}

func TestHookMemoryWalkSkipsAMemoryTheServerRefuses(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	writeMemory(t, dir, "a", "A", "project", "2026-09-17T10:00:00.000Z", "a")
	refused := fmt.Sprintf(memoryFile, "research/notes", "B", "user", "2026-09-17T10:00:00.000Z", "b")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.md"), []byte(refused), 0o644))
	writeMemory(t, dir, "c", "C", "project", "2026-09-17T10:00:00.000Z", "c")
	f.api.refused["research/notes"] = errInvalid(`key "research/notes" may not hold / or :`)

	f.run(t, "session-start", hookInput{SessionID: "s", Cwd: "/repo"})

	marker := f.state.LoadMarker(dir)
	require.Contains(t, marker, "a")
	require.Contains(t, marker, "c", "a name the server refuses for good must not stop the files after it")
	require.NotContains(t, marker, "research/notes", "it is tried again on the next start")
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "b.md")
}

func TestHookMemoryWalkDropsARemovalTheServerRefuses(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	require.NoError(t, f.state.SaveMarker(dir, map[string]markerEntry{
		"old/name": {Modified: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Description: "Old", Type: "user"},
	}))
	f.api.refused["old/name"] = errInvalid(`key "old/name" may not hold / or :`)

	f.run(t, "session-start", hookInput{SessionID: "s", Cwd: "/repo"})

	require.Equal(t, []string{"note:old/name"}, f.api.sent, "the removal was tried")
	require.NotContains(t, f.state.LoadMarker(dir), "old/name", "a removal the server refuses for good must not block the walk on every start")
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "old/name")
}

func TestHookSpoolsAMemoryWriteWhenTheServerIsDown(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	p := writeMemory(t, dir, "late", "Late", "feedback", "2026-09-17T10:00:00.000Z", "body")
	f.api.down = true
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + p + `"}`)})
	entries, err := os.ReadDir(f.state.spool())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Empty(t, f.globalNotes(t, api.KindDecision, "late"))

	f.api.down = false
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "done"})
	require.Len(t, f.globalNotes(t, api.KindDecision, "late"), 1, "the spooled note was replayed")
	entries, _ = os.ReadDir(f.state.spool())
	require.Empty(t, entries)
}

func TestHookSyncsMemoryFromTheConfiguredDirectories(t *testing.T) {
	f := newHookFixture(t)
	settings := filepath.Join(t.TempDir(), "shared")
	require.NoError(t, os.MkdirAll(settings, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.config, "settings.json"), []byte(`{"autoMemoryDirectory":"`+settings+`"}`), 0o644))
	extra := filepath.Join(t.TempDir(), "extra")
	require.NoError(t, os.MkdirAll(extra, 0o755))
	f.r.memory.extra = []string{extra}
	writeMemory(t, settings, "from-settings", "Settings memory", "project", "2026-09-17T10:00:00.000Z", "one")
	writeMemory(t, extra, "from-extra", "Extra memory", "project", "2026-09-17T10:00:00.000Z", "two")

	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Len(t, f.notes(t, api.KindNote, "from-settings"), 1)
	require.Len(t, f.notes(t, api.KindNote, "from-extra"), 1)

	p := writeMemory(t, settings, "written-now", "Written now", "project", "2026-09-17T11:00:00.000Z", "three")
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + p + `"}`)})
	require.Len(t, f.notes(t, api.KindNote, "written-now"), 1)
	require.Empty(t, f.logs)
}

func TestHookMemoryWalkSkipsAFileItCannotDecode(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	broken := filepath.Join(dir, "aaa-broken.md")
	require.NoError(t, os.WriteFile(broken, []byte("---\nname: broken\n"), 0o644))
	writeMemory(t, dir, "zzz-good", "Good", "project", "2026-09-17T10:00:00.000Z", "body")

	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Len(t, f.notes(t, api.KindNote, "zzz-good"), 1, "the walk continues past the broken file")
	require.NotContains(t, f.state.LoadMarker(dir), "broken")
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "cannot decode")

	f.logs = nil
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + broken + `"}`)})
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "cannot decode")
	require.Empty(t, f.out.String())
}

func TestHookMemoryWithoutADescriptionIsTitledByItsName(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	writeMemory(t, dir, "no-description", "", "project", "2026-09-17T10:00:00.000Z", "body")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	notes := f.notes(t, api.KindNote, "no-description")
	require.Len(t, notes, 1)
	require.Equal(t, "no-description", notes[0].Title)
}

func TestHookMemoryRemovalKeepsTheOriginalKind(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	p := writeMemory(t, dir, "gone", "Gone", "feedback", "2026-09-17T10:00:00.000Z", "body")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Len(t, f.globalNotes(t, api.KindDecision, "gone"), 1)

	require.NoError(t, os.Remove(p))
	f.run(t, "session-start", hookInput{SessionID: "d", Cwd: "/repo"})
	versions := f.globalNotes(t, api.KindDecision, "gone")
	require.Len(t, versions, 2, "the removal is a decision too")
	removal := versions[1]
	require.Contains(t, removal.Tags, "removed")
	require.Equal(t, "Gone", removal.Title)
	require.Equal(t, "feedback", removal.Props["memoryType"])
	require.Empty(t, f.notes(t, api.KindNote, "gone"))
}

func TestHookMemoryWalkRemovesNothingWhileAFileCannotBeRead(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	p := writeMemory(t, dir, "kept", "Kept", "project", "2026-09-17T10:00:00.000Z", "body")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Len(t, f.notes(t, api.KindNote, "kept"), 1)

	require.NoError(t, os.WriteFile(p, []byte("---\nname: [broken\n---\nbody\n"), 0o644))
	f.run(t, "session-start", hookInput{SessionID: "d", Cwd: "/repo"})
	versions := f.notes(t, api.KindNote, "kept")
	require.Len(t, versions, 1, "a file that fails to decode is not a removed memory")
	require.NotContains(t, versions[0].Tags, "removed")
}

func TestHookMemoryWalkRemovesNothingWhileAFileCannotBeOpened(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	p := writeMemory(t, dir, "gone", "Gone", "project", "2026-09-17T10:00:00.000Z", "body")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})

	require.NoError(t, os.Remove(p))
	link := filepath.Join(dir, "aaa-dangling.md")
	require.NoError(t, os.Symlink(filepath.Join(dir, "missing.md"), link))
	f.run(t, "session-start", hookInput{SessionID: "d", Cwd: "/repo"})
	require.Len(t, f.notes(t, api.KindNote, "gone"), 1, "the file that cannot be opened may be the one that moved")
	require.Contains(t, f.state.LoadMarker(dir), "gone")

	require.NoError(t, os.Remove(link))
	f.run(t, "session-start", hookInput{SessionID: "e", Cwd: "/repo"})
	require.Len(t, f.notes(t, api.KindNote, "gone"), 2, "the removal goes out once every file reads")
}

func TestHookSessionStartWalksMemoryAfterAClear(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	first := f.session(t, "c").ID

	writeMemory(t, dir, "after-clear", "After the clear", "project", "2026-09-17T10:00:00.000Z", "body")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "clear"})
	require.NotEqual(t, first, f.session(t, "c").ID)
	notes := f.notes(t, api.KindNote, "after-clear")
	require.Len(t, notes, 1)
	require.Equal(t, f.session(t, "c").ID, notes[0].SessionID, "the new session records the walk")
}

func TestHookMemoryWalkWithoutADirectoryDoesNothing(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	s := f.session(t, "c")
	require.Empty(t, s.Notes)
	require.NoDirExists(t, f.state.memory())
	require.Empty(t, f.logs)
	require.NoError(t, f.r.send(f.ctx, spooled{Op: "note", SessionID: s.ID}), "a spooled note without content is dropped")
	require.Zero(t, f.api.called("Note"))
}

func TestHookMemoryMarkerIsPrivate(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	writeMemory(t, dir, "fact", "A fact", "project", "2026-09-17T10:00:00.000Z", "body")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})

	info, err := os.Stat(f.state.memory())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	marker, err := os.Stat(f.state.markerPath(dir))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), marker.Mode().Perm())
}

func TestHookSessionStartJoinsTheProjectOfTheMainWorktree(t *testing.T) {
	f := newHookFixture(t)
	f.r.gitOps.remote = func(context.Context, string) string { return "" }
	f.r.gitOps.worktrees = func(_ context.Context, cwd string) []string { return []string{"/main", cwd} }
	f.run(t, "session-start", hookInput{SessionID: "a", Cwd: "/main"})
	f.run(t, "session-start", hookInput{SessionID: "b", Cwd: "/main-feature"})
	for _, claudeID := range []string{"a", "b"} {
		start := f.session(t, claudeID).Start
		require.Equal(t, "/main", start.RepoRoot, "a worktree session names the main worktree, so the server finds the one project by path")
		require.Equal(t, "/main", start.Cwd)
		require.Empty(t, start.RepoRemote)
	}
}

func TestHookToolUseIgnoresReadingAFile(t *testing.T) {
	f := newHookFixture(t)
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "auth.go"), nil, 0o644))
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: cwd})
	for _, tool := range []string{"Read", "Glob", "Grep", "NotebookRead"} {
		f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: cwd, ToolName: tool, ToolInput: json.RawMessage(`{"file_path":"` + cwd + `/auth.go"}`)})
	}
	require.Zero(t, f.api.called("Artifacts"))
	require.Empty(t, f.out.String())
}

func TestHookSessionStartOutsideARepositoryRecordsNothing(t *testing.T) {
	f := newHookFixture(t)
	f.r.gitOps.remote = func(context.Context, string) string { return "" }
	f.r.gitOps.worktrees = func(context.Context, string) []string { return nil }
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/plain"})
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/plain", Prompt: "hello"})
	f.run(t, "session-end", hookInput{SessionID: "c", Cwd: "/plain"})
	_, ok := f.state.Load("c")
	require.False(t, ok, "no session is mapped")
	require.Empty(t, f.out.String(), "the model is told nothing")
	require.Empty(t, f.logs, "skipping is not a failure")
	require.Empty(t, f.api.calls, "a folder outside a repository never reaches the server")
}

func TestHookSessionStartWithARemoteButNoWorktreesUsesCwd(t *testing.T) {
	f := newHookFixture(t)
	f.r.gitOps.worktrees = nil
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo/sub"})
	start := f.session(t, "c").Start
	require.Equal(t, "/repo/sub", start.Cwd)
	require.Equal(t, testRemote, start.RepoRemote)
	require.Empty(t, start.RepoRoot, "no root is sent when git gives none")
}

func TestHookSessionEndKeepsTheMappingUntilTheServerAnswers(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	id, _ := f.state.Load("c")

	f.api.down = true
	require.Error(t, f.r.run(f.ctx, "session-end", hookInput{SessionID: "c", Cwd: "/repo"}))
	got, ok := f.state.Load("c")
	require.True(t, ok, "a failed end keeps the mapping so a resume finds the open session")
	require.Equal(t, id, got)

	f.api.down = false
	f.run(t, "session-end", hookInput{SessionID: "c", Cwd: "/repo"})
	_, ok = f.state.Load("c")
	require.False(t, ok)
	require.True(t, f.api.byID(id).ended)

	f.run(t, "session-start", hookInput{SessionID: "d", Cwd: "/repo"})
	id, _ = f.state.Load("d")
	f.api.end(id)
	f.run(t, "session-end", hookInput{SessionID: "d", Cwd: "/repo"})
	_, ok = f.state.Load("d")
	require.False(t, ok, "a rejection is final, the mapping goes")

	require.NoError(t, f.state.SaveRecalled("orphan", recallMarker{Files: []string{"a.go"}}))
	f.run(t, "session-end", hookInput{SessionID: "orphan", Cwd: "/repo"})
	require.False(t, recalled(f.state, "orphan"), "an unmapped end still removes its recall marker")
}

func TestHookPreCompactDrainsAndSpoolsLikeTheOtherCaptureHooks(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})

	f.api.down = true
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "did it"})
	f.run(t, "pre-compact", hookInput{SessionID: "c", Cwd: "/repo", Trigger: "auto"})
	entries, err := os.ReadDir(f.state.spool())
	require.NoError(t, err)
	require.Len(t, entries, 2, "the checkpoint is spooled like any capture")
	require.Empty(t, f.api.sent)

	f.api.down = false
	f.run(t, "pre-compact", hookInput{SessionID: "c", Cwd: "/repo", Trigger: "manual"})
	require.Equal(t, []string{"progress:did it", "note:", "note:"}, f.api.sent, "the spool drains before the new checkpoint")
	entries, _ = os.ReadDir(f.state.spool())
	require.Empty(t, entries)
	checkpoints := f.checkpoints()
	require.Len(t, checkpoints, 2)
	require.Contains(t, checkpoints[0].Body, "Context compaction (auto)")
	require.Contains(t, checkpoints[1].Body, "Context compaction (manual)")
}

func TestHookCapsTheDiffStatBeforeSendingIt(t *testing.T) {
	f := newHookFixture(t)
	f.r.gitOps.diffStat = func(context.Context, string) string {
		return strings.Repeat(" internal/auth.go | 2 +-\n", api.MaxBodyBytes/20)
	}
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	s := f.session(t, "c")
	f.run(t, "pre-compact", hookInput{SessionID: "c", Cwd: "/repo", Trigger: "auto"})
	f.run(t, "session-end", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Empty(t, f.logs, "nothing is rejected")

	require.Len(t, s.Progress, 1, "a diff past the message cap is cut, not dropped")
	require.Len(t, s.Progress[0].Message, api.MaxMessageBytes)
	checkpoints := f.checkpoints()
	require.Len(t, checkpoints, 1)
	require.Len(t, checkpoints[0].Body, api.MaxBodyBytes)
}

func TestSpoolDrainCountsSendsAndLeavesEntriesInFlightAlone(t *testing.T) {
	state := newHookState(t.TempDir())
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now.Add(-8 * 24 * time.Hour) }
	for i := 0; i < spoolDrainLimit; i++ {
		require.NoError(t, state.Spool(spooled{Op: "progress", Message: "old"}))
	}
	state.now = func() time.Time { return now }
	require.NoError(t, state.Spool(spooled{Op: "progress", Message: "live"}))
	require.NoError(t, state.Spool(spooled{Op: "progress", Message: "taken"}))
	entries, err := os.ReadDir(state.spool())
	require.NoError(t, err)
	taken := filepath.Join(state.spool(), entries[len(entries)-1].Name())
	require.NoError(t, os.Rename(taken, taken+".sending"))

	var seen []string
	send := func(item spooled) error { seen = append(seen, item.Message); return nil }
	sent, err := state.Drain(send)
	require.NoError(t, err)
	require.Equal(t, 1, sent)
	require.Equal(t, []string{"live"}, seen, "expired entries do not use up the sixteen and an entry in flight is skipped")
	entries, _ = os.ReadDir(state.spool())
	require.Len(t, entries, 1)
	require.Equal(t, filepath.Base(taken)+".sending", entries[0].Name())

	stale := now.Add(-2 * spoolStaleSending)
	require.NoError(t, os.Chtimes(taken+".sending", stale, stale))
	seen = nil
	sent, err = state.Drain(send)
	require.NoError(t, err)
	require.Equal(t, 1, sent)
	require.Equal(t, []string{"taken"}, seen, "an entry a killed hook left in flight is taken back")
	entries, _ = os.ReadDir(state.spool())
	require.Empty(t, entries)
}

func TestHookSessionStartDrainsTheSpool(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})

	f.api.down = true
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "did it"})
	entries, err := os.ReadDir(f.state.spool())
	require.NoError(t, err)
	require.Len(t, entries, 1)

	f.api.down = false
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "resume"})
	require.Equal(t, []string{"progress:did it"}, f.api.sent, "the next session start sends what the last one could not")
	entries, _ = os.ReadDir(f.state.spool())
	require.Empty(t, entries)
}

func TestHookRedactsTheDiffStat(t *testing.T) {
	f := newHookFixture(t)
	f.r.gitOps.diffStat = func(context.Context, string) string {
		return " secrets/token=sk-ant-api03-abcdefghijklmnopqrstuvwxyz | 2 +-"
	}
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	s := f.session(t, "c")
	f.run(t, "pre-compact", hookInput{SessionID: "c", Cwd: "/repo", Trigger: "auto"})
	f.run(t, "session-end", hookInput{SessionID: "c", Cwd: "/repo"})

	require.Len(t, s.Progress, 1)
	require.NotContains(t, s.Progress[0].Message, "sk-ant-api03")
	require.Contains(t, s.Progress[0].Message, "[redacted:")

	checkpoints := f.checkpoints()
	require.Len(t, checkpoints, 1)
	require.NotContains(t, checkpoints[0].Body, "sk-ant-api03")
}

func TestHookCwdUpdatesTheBranch(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	s := f.session(t, "c")
	require.Equal(t, "main", s.Start.Branch)

	f.r.gitOps.branch = func(context.Context, string) string { return "feat/x" }
	f.run(t, "cwd", hookInput{SessionID: "c", Cwd: "/repo/api"})
	require.Equal(t, []api.SessionLocationInput{{Cwd: "/repo/api", RepoRemote: testRemote, RepoRoot: "/repo/api", Branch: "feat/x"}}, s.Locations)

	f.run(t, "cwd", hookInput{SessionID: "unknown", Cwd: "/repo"})
	require.Equal(t, 1, f.api.called("SessionLocation"), "an unmapped session sends nothing")
	require.Empty(t, f.logs, "an unmapped session is silent")
	require.Empty(t, f.out.String(), "the cwd hook prints nothing")
}

func TestHookCwdOutsideTheProjectKeepsTheSession(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	id, _ := f.state.Load("c")
	f.r.gitOps.remote = func(context.Context, string) string { return "git@github.com:other/repo.git" }
	f.api.errs["SessionLocation"] = errInvalid(`cwd "/elsewhere" is outside the session's project`)
	f.run(t, "cwd", hookInput{SessionID: "c", Cwd: "/elsewhere"})
	got, ok := f.state.Load("c")
	require.True(t, ok, "the session goes on where it was")
	require.Equal(t, id, got)
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "cwd: location not recorded")
	sent, err := f.state.Drain(func(spooled) error { return nil })
	require.NoError(t, err)
	require.Zero(t, sent, "a location is never spooled")
}

func TestHookCwdWarnsWhenTheServerIsDown(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	f.api.down = true
	f.run(t, "cwd", hookInput{SessionID: "c", Cwd: "/repo/api"})
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "cwd: location not recorded")
	sent, err := f.state.Drain(func(spooled) error { return nil })
	require.NoError(t, err)
	require.Zero(t, sent, "an unreachable server never spools a location")
}

func TestHookCwdSendsTheMainWorktreeRoot(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	s := f.session(t, "c")

	f.r.gitOps.worktrees = func(_ context.Context, cwd string) []string { return []string{"/repo", cwd} }
	f.run(t, "cwd", hookInput{SessionID: "c", Cwd: "/repo-feature"})
	require.Equal(t, "/repo", s.Locations[0].Cwd, "the main worktree root goes out, not the raw cwd")
	require.Equal(t, "/repo", s.Locations[0].RepoRoot)

	f.r.gitOps.worktrees = nil
	f.run(t, "cwd", hookInput{SessionID: "c", Cwd: "/repo/api"})
	require.Equal(t, "/repo/api", s.Locations[1].Cwd)

	f.r.gitOps.worktrees = func(context.Context, string) []string { return nil }
	f.run(t, "cwd", hookInput{SessionID: "c", Cwd: "/repo/web"})
	require.Equal(t, "/repo/web", s.Locations[2].Cwd, "an empty worktree list falls back to the cwd")
	require.Empty(t, s.Locations[2].RepoRoot)
	require.Empty(t, f.logs)
}

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "transcript.jsonl")
	require.NoError(t, os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
	return p
}

func TestHookSendsTheSessionNameFromTheTranscript(t *testing.T) {
	f := newHookFixture(t)
	first := writeTranscript(t,
		`{"type":"user","slug":"fix-the-login-bug-mutable-penguin"}`,
		`{"type":"assistant","slug":"fix-the-login-bug-mutable-penguin"}`,
	)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"})
	s := f.session(t, "c")
	require.Empty(t, s.Start.HarnessSessionID, "an id claude code would not take is not sent")

	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "first turn", TranscriptPath: first})
	require.Equal(t, api.ProgressInput{Message: "first turn", Name: "fix-the-login-bug-mutable-penguin"}, s.Progress[0])
	require.Equal(t, "fix-the-login-bug-mutable-penguin", f.state.LoadName("c"))

	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "second turn", TranscriptPath: first})
	require.Equal(t, api.ProgressInput{Message: "second turn"}, s.Progress[1], "a name the server has is not sent again")

	renamed := writeTranscript(t, `{"type":"user","slug":"rotate-the-token-brave-otter"}`)
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "third turn", TranscriptPath: renamed})
	require.Equal(t, "rotate-the-token-brave-otter", s.Progress[2].Name)
	require.Equal(t, "rotate-the-token-brave-otter", f.state.LoadName("c"))

	f.run(t, "session-end", hookInput{SessionID: "c", Cwd: "/repo", Reason: "other"})
	require.Empty(t, f.state.LoadName("c"), "the name goes with the mapping")
}

func TestTranscriptNameReadsTheTailAndSurvivesRubbish(t *testing.T) {
	require.Empty(t, transcriptName(""), "no transcript path")
	require.Empty(t, transcriptName(filepath.Join(t.TempDir(), "gone.jsonl")), "unreadable path")
	require.Empty(t, transcriptName(writeTranscript(t, `{"type":"user"}`, "not json")), "no slug in the transcript")
	require.Equal(t, "last-one-wins", transcriptName(writeTranscript(t,
		`{"slug":"first-one"}`,
		`{"slug":"last-one-wins"}`,
	)))

	long := []string{`{"slug":"too-old-to-read"}`}
	for len(strings.Join(long, "\n")) < transcriptTail {
		long = append(long, `{"type":"assistant","message":"`+strings.Repeat("x", 512)+`"}`)
	}
	long = append(long, `{"slug":"within-the-tail"}`)
	require.Equal(t, "within-the-tail", transcriptName(writeTranscript(t, long...)), "only the tail is read")
}

func TestTranscriptNamePrefersTheTitleThePersonSees(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{"an ai title without plan mode", []string{
			`{"type":"user","message":"add Divide"}`,
			`{"type":"ai-title","aiTitle":"Divide function for calc","sessionId":"x"}`,
			`{"type":"assistant","message":"done"}`,
		}, "Divide function for calc"},
		{"the newest ai title", []string{
			`{"type":"ai-title","aiTitle":"First guess"}`,
			`{"type":"ai-title","aiTitle":"Better guess"}`,
		}, "Better guess"},
		{"a rename wins over a later ai title", []string{
			`{"type":"custom-title","customTitle":"INFRA-4363"}`,
			`{"type":"ai-title","aiTitle":"arp guard"}`,
			`{"type":"user","slug":"plan-file-name"}`,
		}, "INFRA-4363"},
		{"the agent name wins over a later ai title", []string{
			`{"type":"agent-name","agentName":"fix-bellek-field-issues"}`,
			`{"type":"ai-title","aiTitle":"Next item on PLAN.md"}`,
		}, "fix-bellek-field-issues"},
		{"a rename wins over the agent name", []string{
			`{"type":"custom-title","customTitle":"INFRA-4363"}`,
			`{"type":"agent-name","agentName":"arp-guard-l2-anti-spoof"}`,
		}, "INFRA-4363"},
		{"a later rename wins over the agent name", []string{
			`{"type":"agent-name","agentName":"arp-guard-l2-anti-spoof"}`,
			`{"type":"custom-title","customTitle":"INFRA-4363"}`,
		}, "INFRA-4363"},
		{"the newest agent name", []string{
			`{"type":"agent-name","agentName":"first-name"}`,
			`{"type":"agent-name","agentName":"second-name"}`,
		}, "second-name"},
		{"the agent name wins over an older ai title and the slug", []string{
			`{"type":"user","slug":"plan-file-name"}`,
			`{"type":"ai-title","aiTitle":"Plan the fix"}`,
			`{"type":"agent-name","agentName":"fix-bellek-field-issues"}`,
		}, "fix-bellek-field-issues"},
		{"an empty agent name falls through", []string{
			`{"type":"ai-title","aiTitle":"Plan the fix"}`,
			`{"type":"agent-name","agentName":""}`,
		}, "Plan the fix"},
		{"an ai title wins over the plan slug", []string{
			`{"type":"user","slug":"plan-file-name"}`,
			`{"type":"ai-title","aiTitle":"Plan the fix"}`,
		}, "Plan the fix"},
		{"the slug when nothing else is there", []string{
			`{"type":"user","slug":"plan-file-name"}`,
		}, "plan-file-name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, transcriptName(writeTranscript(t, tt.lines...)))
		})
	}
}

func TestHookKeepsTheNameWhenTheServerIsDown(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"})
	s := f.session(t, "c")
	path := writeTranscript(t, `{"slug":"named-while-offline"}`)

	f.api.down = true
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "a turn", TranscriptPath: path})
	require.Empty(t, f.state.LoadName("c"), "a failed send is not remembered")

	f.api.down = false
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "another turn", TranscriptPath: path})
	require.Equal(t, api.ProgressInput{Message: "another turn", Name: "named-while-offline"}, s.Progress[len(s.Progress)-1], "the name comes again on the next turn")
	require.Equal(t, "named-while-offline", f.state.LoadName("c"))
}

func TestHookSendsTheNameAgainWhenTheMarkerCannotBeWritten(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"})
	s := f.session(t, "c")
	path := writeTranscript(t, `{"slug":"unwritable-marker-calm-heron"}`)
	blocked, ok := f.state.namePath("c")
	require.True(t, ok)
	require.NoError(t, os.MkdirAll(blocked, 0o700))

	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "a turn", TranscriptPath: path})
	require.Equal(t, "unwritable-marker-calm-heron", s.Progress[0].Name, "the name still reaches the server")
	require.Empty(t, f.state.LoadName("c"))
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "session name not remembered")

	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "another turn", TranscriptPath: path})
	require.Len(t, f.logs, 2)
	require.Len(t, s.Progress, 2, "capture keeps working")
	require.Equal(t, "unwritable-marker-calm-heron", s.Progress[1].Name, "without a marker every turn sends the name again")
}

func TestSessionNameStateRejectsIdsThatLeaveTheDirectory(t *testing.T) {
	s := newHookState(t.TempDir())
	for _, bad := range []string{"", "..", "../escape", "a/b", `a\b`} {
		require.ErrorIs(t, s.SaveName(bad, "a-name"), os.ErrInvalid, bad)
		require.Empty(t, s.LoadName(bad), bad)
	}
	require.NoError(t, s.SaveName("c", "a-name"))
	require.Equal(t, "a-name", s.LoadName("c"))
}

func TestSpoolNamesFilesFromTheInjectedClock(t *testing.T) {
	state := newHookState(t.TempDir())
	state.now = func() time.Time { return time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC) }
	require.NoError(t, state.Spool(spooled{Op: "progress", Message: "one"}))
	require.NoError(t, state.Spool(spooled{Op: "progress", Message: "two"}))

	entries, err := os.ReadDir(state.spool())
	require.NoError(t, err)
	require.Len(t, entries, 2, "a fixed clock still gives each entry its own file")
	want := fmt.Sprintf("%020d", time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC).UnixNano())
	for _, e := range entries {
		require.True(t, strings.HasPrefix(e.Name(), want), e.Name())
	}
	require.True(t, entries[0].Name() < entries[1].Name(), "the counter keeps replay order")
}

func recalled(s *hookState, claudeID string) bool {
	return s.Recalled(claudeID).Instructed
}

func marker(s *hookState, claudeID string) recallMarker {
	return s.Recalled(claudeID)
}

func TestHookSessionEndSaysWhyItEnded(t *testing.T) {
	for _, tc := range []struct{ reason, want string }{
		{"clear", "session ended by claude code: clear"},
		{"prompt_input_exit", "session ended by claude code: prompt_input_exit"},
		{"", "session ended by claude code"},
	} {
		t.Run("reason "+tc.reason, func(t *testing.T) {
			f := newHookFixture(t)
			f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
			s := f.session(t, "c")
			f.run(t, "session-end", hookInput{SessionID: "c", Cwd: "/repo", Reason: tc.reason})
			require.Equal(t, tc.want, s.EndReason)
		})
	}
}

func TestHookRunsWithAGitOpsThatAnswersOnlyTheRemote(t *testing.T) {
	f := newHookFixture(t)
	f.r.gitOps = gitOps{remote: func(context.Context, string) string { return testRemote }}

	for _, event := range hookEvents {
		if event == "session-end" {
			continue
		}
		f.run(t, event, hookInput{SessionID: "c", Cwd: "/repo", Prompt: "start", Trigger: "auto"})
	}
	s := f.session(t, "c")
	require.Empty(t, s.Start.Branch, "no branch query, no branch")

	f.run(t, "session-end", hookInput{SessionID: "c", Cwd: "/repo"})
	require.True(t, s.ended)
}

func TestGitMainRootFindsTheMainWorktree(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "--quiet")
	git("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "--quiet", "--allow-empty", "-m", "first")
	linked := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "--quiet", "-b", "topic", linked)
	nested := filepath.Join(repo, "internal", "auth")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	want, err := filepath.EvalSymlinks(repo)
	require.NoError(t, err)

	tests := []struct {
		name string
		cwd  string
		want string
	}{
		{"the root itself", repo, want},
		{"a directory inside it", nested, want},
		{"a linked worktree", linked, want},
		{"outside any repository", t.TempDir(), ""},
		{"no directory at all", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gitMainRoot(tt.cwd)
			if got != "" {
				got, err = filepath.EvalSymlinks(got)
				require.NoError(t, err)
			}
			require.Equal(t, tt.want, got)
		})
	}
}

func TestHookCommandExitsZeroWhenTheConfigIsBroken(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("BELLEK_TOKEN", "")
	stdout, stderr := captureStd(t, func() {
		out, err := runWithStdin(t, `{"session_id":"claude-1","cwd":"/repo"}`, "hook", "session-start")
		require.NoError(t, err, "a hook must never fail the agent it runs inside")
		require.Empty(t, out)
	})
	require.Empty(t, stdout, "stdout carries the answer to Claude Code and nothing else")
	require.Contains(t, stderr, "bellek hook:")
	require.Contains(t, stderr, "BELLEK_TOKEN")
}

func TestNewHookRunnerWarnsOnStderr(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("BELLEK_STATE_DIR", t.TempDir())
	var answers bytes.Buffer
	stdout, stderr := captureStd(t, func() {
		a, err := newAppWith(stderrLogger)
		require.NoError(t, err)
		defer a.close()
		r := newHookRunner(a, &answers, hookTimeout)
		require.Same(t, a.cfg, r.cfg)
		require.NotNil(t, r.api)
		r.warn("tool-use", "capture spooled: %v", errors.New("connection refused"))
	})
	require.Empty(t, answers.String())
	require.Empty(t, stdout)
	require.Contains(t, stderr, "capture spooled: connection refused")
	require.Contains(t, stderr, "tool-use")
}

func TestNewHookRunnerClientStopsAtItsBudget(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("BELLEK_STATE_DIR", t.TempDir())
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	t.Setenv("BELLEK_URL", srv.URL)
	a, err := newAppWith(stderrLogger)
	require.NoError(t, err)
	defer a.close()

	r := newHookRunner(a, &bytes.Buffer{}, 100*time.Millisecond)
	began := time.Now()
	_, err = r.api.SessionStart(context.Background(), api.SessionStartInput{Cwd: "/repo"})
	require.Less(t, time.Since(began), 2*time.Second, "the runner's client must use the budget, not the client default")
	var timeout *url.Error
	require.ErrorAs(t, err, &timeout)
	require.True(t, timeout.Timeout(), "session start reports a slow server through this timeout error")
}

func TestHookSessionStartWarnsWhenTheClearedSessionCannotBeEnded(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"})
	first := f.session(t, "c").ID
	f.api.errs["SessionEndReason"] = errUnreachable

	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "clear"})
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "could not end the cleared session")
	require.NotEqual(t, first, f.session(t, "c").ID, "a cleared context starts over even when the old session stays open")
}

func TestHookToolUseWithoutASessionRecordsNothing(t *testing.T) {
	f := newHookFixture(t)
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "main.go"), []byte("package main"), 0o644))

	f.run(t, "tool-use", hookInput{SessionID: "unknown", Cwd: cwd, ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":"` + cwd + `/main.go"}`)})
	require.Empty(t, f.out.String())
	require.Empty(t, f.logs)
	require.Empty(t, f.api.calls)
	_, err := os.ReadDir(f.state.spool())
	require.True(t, os.IsNotExist(err), "an edit with no session behind it is dropped, not spooled")
}

func TestHookToolUseNamesTheFileEvenWhenTheMarkerCannotBeWritten(t *testing.T) {
	f := newHookFixture(t)
	f.recall("files")
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "auth.go"), nil, 0o644))
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: cwd})
	f.api.links["auth.go"] = []api.Ref{taskRef("auth-refresh")}
	require.NoError(t, os.WriteFile(f.state.recall(), []byte("not a directory"), 0o600))

	edit := hookInput{SessionID: "c", Cwd: cwd, ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":"` + cwd + `/auth.go"}`)}
	f.run(t, "tool-use", edit)
	require.Contains(t, f.answer(t, "PostToolUse"), "is linked to task repo-1 auth-refresh")
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "recall marker not written")

	f.run(t, "tool-use", edit)
	require.Contains(t, f.answer(t, "PostToolUse"), "is linked to task repo-1 auth-refresh", "without a marker the next edit names it again")
}

func TestHookSendIgnoresAnOpItDoesNotKnow(t *testing.T) {
	f := newHookFixture(t)
	require.NoError(t, f.state.Spool(spooled{Op: "dance", SessionID: uuid.New(), Message: "hi"}))

	f.r.drain(f.ctx)
	require.Empty(t, f.api.calls, "an entry the hook cannot replay reaches no endpoint")
	require.Empty(t, f.logs)
	entries, err := os.ReadDir(f.state.spool())
	require.NoError(t, err)
	require.Empty(t, entries, "and it is dropped rather than replayed forever")
}

func TestHookSaysSoWhenTheSpoolItselfFails(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	f.api.down = true
	require.NoError(t, os.WriteFile(f.state.spool(), []byte("not a directory"), 0o600))

	f.run(t, "stop", hookInput{SessionID: "c", Cwd: "/repo", LastAssistantMessage: "did it"})
	require.Len(t, f.logs, 2)
	require.Contains(t, f.logs[0], "spool replay stopped")
	require.Contains(t, f.logs[1], "capture lost, spool failed")
	require.Contains(t, f.logs[1], "connection refused", "the log names the capture it could not keep")
}

func TestHookMemorySyncReportsAFileThatIsGone(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	gone := filepath.Join(dir, "vanished.md")

	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + gone + `"}`)})
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "cannot read")
	require.Contains(t, f.logs[0], gone)
	require.Empty(t, f.out.String())
	require.NotContains(t, f.state.LoadMarker(dir), "vanished")
	require.Zero(t, f.api.called("Note"))
}

func TestHookMemorySyncReportsAMarkerItCannotSave(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	dir := f.memoryDir(t)
	p := writeMemory(t, dir, "kept", "Kept", "project", "2026-09-17T10:00:00.000Z", "body")
	require.NoError(t, os.WriteFile(f.state.memory(), []byte("not a directory"), 0o600))

	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Write", ToolInput: json.RawMessage(`{"file_path":"` + p + `"}`)})
	require.Len(t, f.notes(t, api.KindNote, "kept"), 1, "the note still reaches the server")
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "marker not saved")
}

func TestHookMemoryWalkStopsWhenTheMarkerCannotBeSaved(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	writeMemory(t, dir, "a", "A", "project", "2026-09-17T10:00:00.000Z", "body")
	require.NoError(t, os.WriteFile(f.state.memory(), []byte("not a directory"), 0o600))

	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Len(t, f.notes(t, api.KindNote, "a"), 1, "the note went out before the marker failed")
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "walk of "+dir+" stopped")
}

func TestHookMemoryWalkSkipsAFileItCannotRead(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	require.NoError(t, os.Symlink(filepath.Join(dir, "missing.md"), filepath.Join(dir, "aaa-dangling.md")))
	writeMemory(t, dir, "zzz-good", "Good", "project", "2026-09-17T10:00:00.000Z", "body")

	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Len(t, f.notes(t, api.KindNote, "zzz-good"), 1, "the walk carries on past the file it cannot open")
	require.Empty(t, f.logs)
	require.NotContains(t, f.state.LoadMarker(dir), "aaa-dangling")
}

func TestHookMemoryWalkReportsARemovalItCannotSend(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	writeMemory(t, dir, "gone", "Gone", "project", "2026-09-17T10:00:00.000Z", "body")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Contains(t, f.state.LoadMarker(dir), "gone")

	require.NoError(t, os.Remove(filepath.Join(dir, "gone.md")))
	f.api.down = true
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "resume"})

	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "gone removed")
	require.Contains(t, f.state.LoadMarker(dir), "gone", "the entry stays so the next session tries again")
}

func TestHookRedactsTheLocationAndNameItSends(t *testing.T) {
	f := newHookFixture(t)
	f.r.gitOps.remote = func(context.Context, string) string {
		return "https://x-access-token:secret-for-test@github.com/o/r.git"
	}
	f.r.gitOps.branch = func(context.Context, string) string { return "fix/password=abcdefghijklmn" }
	cwd := "/home/ci/token=abcdefghijklmnop/repo"
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: cwd, Source: "startup"})

	s := f.session(t, "c")
	require.NotContains(t, s.Start.RepoRemote, "secret-for-test")
	require.NotContains(t, s.Start.Cwd, "abcdefghijklmnop")
	require.NotContains(t, s.Start.RepoRoot, "abcdefghijklmnop")
	require.NotContains(t, s.Start.Branch, "abcdefghijklmn")

	f.r.gitOps.branch = func(context.Context, string) string { return "fix/secret=abcdefghijklmnopq" }
	f.run(t, "cwd", hookInput{SessionID: "c", Cwd: cwd})
	require.Len(t, s.Locations, 1)
	require.Contains(t, s.Locations[0].Branch, "[redacted:", "the location route got the new branch, redacted")
	require.NotContains(t, s.Locations[0].Branch, "abcdefghijklmnopq")
	require.NotContains(t, s.Locations[0].Cwd, "abcdefghijklmnop")
	require.NotContains(t, s.Locations[0].RepoRemote, "secret-for-test")

	transcript := writeTranscript(t, `{"type":"user","slug":"rotate-token=abcdefghijklmnop-brave-otter"}`)
	f.run(t, "stop", hookInput{SessionID: "c", Cwd: cwd, LastAssistantMessage: "done", TranscriptPath: transcript})
	require.Len(t, s.Progress, 1)
	require.NotEmpty(t, s.Progress[0].Name)
	require.NotContains(t, s.Progress[0].Name, "abcdefghijklmnop")
}

func TestHookSendsAUUIDSessionIDAsTheHarnessID(t *testing.T) {
	f := newHookFixture(t)
	id := "c0ffee00-1111-2222-3333-444455556666"
	f.run(t, "session-start", hookInput{SessionID: id, Cwd: "/repo", Source: "startup"})
	require.Equal(t, id, f.session(t, id).Start.HarnessSessionID, "the id that resumes the session in claude code")
}

func TestHookCapturesFilesABashCallChanged(t *testing.T) {
	f := newHookFixture(t)
	cwd := t.TempDir()
	file := func(name, body string) string {
		p := filepath.Join(cwd, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
		return p
	}
	busy := file("busy.go", "package busy")
	f.dirty = []string{busy}
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: cwd})
	s := f.session(t, "c")
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: cwd, Prompt: "add Divide"})

	bash := hookInput{SessionID: "c", Cwd: cwd, ToolName: "Bash", ToolUseID: "toolu_1", ToolInput: json.RawMessage(`{"command":"cat > calc.go"}`)}
	f.run(t, "pre-tool-use", bash)
	f.dirty = append(f.dirty, file("calc.go", "package calc"), file(".env", "TOKEN=x"))
	f.run(t, "tool-use", bash)

	quiet := hookInput{SessionID: "c", Cwd: cwd, ToolName: "Bash", ToolUseID: "toolu_2", ToolInput: json.RawMessage(`{"command":"cat calc.go"}`)}
	f.run(t, "pre-tool-use", quiet)
	f.run(t, "tool-use", quiet)

	f.dirty = append(f.dirty, file("other.go", "package other"))
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: cwd, ToolName: "Bash", ToolUseID: "toolu_3"})
	require.Equal(t, [][]string{{"calc.go"}}, s.Artifacts, "a file dirty before the call, an ignored file, a call that changed nothing and a call without a snapshot add nothing")

	revert := hookInput{SessionID: "c", Cwd: cwd, ToolName: "Bash", ToolUseID: "toolu_4", ToolInput: json.RawMessage(`{"command":"git checkout busy.go && echo more >> other.go"}`)}
	f.run(t, "pre-tool-use", revert)
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "other.go"), []byte("package other // more"), 0o644))
	f.dirty = []string{filepath.Join(cwd, "calc.go"), filepath.Join(cwd, "other.go")}
	f.run(t, "tool-use", revert)
	require.Equal(t, [][]string{{"calc.go"}, {"busy.go", "other.go"}}, s.Artifacts, "a file made clean and a dirty file written again both count")
}

func TestHookCapturesAFileABashCallWritesInAWorktreeItCreates(t *testing.T) {
	f := newHookFixture(t)
	cwd, added := t.TempDir(), t.TempDir()
	worktrees := []string{cwd}
	dirty := map[string][]string{}
	f.r.gitOps.worktrees = func(context.Context, string) []string { return append([]string{}, worktrees...) }
	f.r.gitOps.status = func(_ context.Context, dir string) []string { return append([]string{}, dirty[dir]...) }
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: cwd})
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: cwd, Prompt: "add a changelog"})

	bash := hookInput{SessionID: "c", Cwd: cwd, ToolName: "Bash", ToolUseID: "toolu_1",
		ToolInput: json.RawMessage(`{"command":"git worktree add ../added main && cd ../added && echo x >> CHANGELOG"}`)}
	f.run(t, "pre-tool-use", bash)
	changelog := filepath.Join(added, "CHANGELOG")
	require.NoError(t, os.WriteFile(changelog, []byte("x\n"), 0o644))
	worktrees = append(worktrees, added)
	dirty[added] = []string{changelog}
	f.run(t, "tool-use", bash)

	require.Equal(t, [][]string{{"CHANGELOG"}}, f.session(t, "c").Artifacts, "a file written in a worktree the call created is keyed relative to that worktree")
}

func TestHookCapturesAFileABashCallWritesInAWorktreeItCreatesWithRealGit(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "--quiet", "--initial-branch=main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README"), []byte("hi\n"), 0o644))
	git("add", "README")
	git("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "--quiet", "-m", "first")

	f := newHookFixture(t)
	f.r.gitOps.worktrees = gitinfo.Worktrees
	f.r.gitOps.status = gitinfo.Status
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: repo})
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: repo, Prompt: "add a changelog"})

	added := filepath.Join(t.TempDir(), "added")
	bash := hookInput{SessionID: "c", Cwd: repo, ToolName: "Bash", ToolUseID: "toolu_1",
		ToolInput: json.RawMessage(`{"command":"git worktree add -b changelog ` + added + ` && echo x > ` + added + `/CHANGELOG"}`)}
	f.run(t, "pre-tool-use", bash)
	git("worktree", "add", "--quiet", "-b", "changelog", added)
	require.NoError(t, os.WriteFile(filepath.Join(added, "CHANGELOG"), []byte("x\n"), 0o644))
	f.run(t, "tool-use", bash)

	require.Equal(t, [][]string{{"CHANGELOG"}}, f.session(t, "c").Artifacts)
}

func TestHookBashWorktreeCapture(t *testing.T) {
	tests := []struct {
		name   string
		before []string
		after  []string
		dirty  map[string][]string
		want   [][]string
	}{
		{"no worktree list before the call", nil, []string{"/main", "/added"}, map[string][]string{"/added": {"/added/CHANGELOG"}}, nil},
		{"a worktree that already existed", []string{"/main", "/old"}, []string{"/main", "/old"}, map[string][]string{"/old": {"/old/CHANGELOG"}}, nil},
		{"a new worktree with no dirty files", []string{"/main"}, []string{"/main", "/added"}, map[string][]string{"/added": {}}, nil},
		{"a new worktree git cannot read", []string{"/main"}, []string{"/main", "/added"}, map[string][]string{}, nil},
		{"only the new worktree counts", []string{"/main", "/old"}, []string{"/main", "/old", "/added"}, map[string][]string{"/old": {"/old/stale.go"}, "/added": {"/added/new.go"}}, [][]string{{"new.go"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newHookFixture(t)
			worktrees := tt.before
			dirty := map[string][]string{"/main": {}}
			f.r.gitOps.worktrees = func(context.Context, string) []string { return worktrees }
			f.r.gitOps.status = func(_ context.Context, dir string) []string { return dirty[dir] }
			f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/main"})
			f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/main", Prompt: "work"})

			bash := hookInput{SessionID: "c", Cwd: "/main", ToolName: "Bash", ToolUseID: "toolu_1"}
			f.run(t, "pre-tool-use", bash)
			worktrees = tt.after
			for dir, paths := range tt.dirty {
				dirty[dir] = paths
			}
			f.run(t, "tool-use", bash)
			require.Equal(t, tt.want, f.session(t, "c").Artifacts)
		})
	}
}

func TestHookBashCaptureNeedsGitOnBothSides(t *testing.T) {
	f := newHookFixture(t)
	cwd := t.TempDir()
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: cwd})
	bash := hookInput{SessionID: "c", Cwd: cwd, ToolName: "Bash", ToolUseID: "toolu_1"}

	f.r.gitOps.status = func(context.Context, string) []string { return nil }
	f.run(t, "pre-tool-use", bash)
	f.r.gitOps.status = func(context.Context, string) []string { return []string{filepath.Join(cwd, "a.go")} }
	f.run(t, "tool-use", bash)

	f.r.gitOps.status = func(context.Context, string) []string { return []string{filepath.Join(cwd, "a.go")} }
	f.run(t, "pre-tool-use", bash)
	f.r.gitOps.status = func(context.Context, string) []string { return nil }
	f.run(t, "tool-use", bash)

	require.Zero(t, f.api.called("Artifacts"), "a failed git status on either side is no answer, not an empty repository")
	entries, _ := os.ReadDir(filepath.Join(f.state.root, "bash"))
	require.Empty(t, entries, "the snapshot is used once")
}

func TestHookBashSnapshotsGoWithTheSession(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	f.run(t, "pre-tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Bash", ToolUseID: "toolu_1"})
	f.run(t, "pre-tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Read", ToolUseID: "toolu_2"})
	f.run(t, "pre-tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Bash", ToolUseID: "../escape"})
	entries, err := os.ReadDir(filepath.Join(f.state.root, "bash"))
	require.NoError(t, err)
	require.Len(t, entries, 1, "only a Bash call with a safe id takes a snapshot")

	old := filepath.Join(f.state.root, "bash", "gone-toolu_9.json")
	require.NoError(t, os.WriteFile(old, []byte("{}"), 0o600))
	past := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(old, past, past))
	f.run(t, "session-start", hookInput{SessionID: "other", Cwd: "/repo"})
	_, err = os.Stat(old)
	require.True(t, os.IsNotExist(err), "a snapshot a killed call left behind is pruned at the next session start")

	f.run(t, "session-end", hookInput{SessionID: "c", Cwd: "/repo"})
	entries, err = os.ReadDir(filepath.Join(f.state.root, "bash"))
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestHookPreToolUseWithoutASessionTakesNoSnapshot(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "pre-tool-use", hookInput{SessionID: "unknown", Cwd: "/repo", ToolName: "Bash", ToolUseID: "toolu_1"})
	_, err := os.Stat(filepath.Join(f.state.root, "bash"))
	require.True(t, os.IsNotExist(err), "a call outside a known session writes nothing")
}

func TestHookBashSnapshotWarnsWhenItCannotBeWritten(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.NoError(t, os.WriteFile(filepath.Join(f.state.root, "bash"), []byte("in the way"), 0o600))
	f.logs = nil
	f.run(t, "pre-tool-use", hookInput{SessionID: "c", Cwd: "/repo", ToolName: "Bash", ToolUseID: "toolu_1"})
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "pre-tool-use: bash snapshot not written")
	require.Empty(t, f.out.String(), "a failed snapshot never reaches the agent")
}

func TestHookBashIgnoresASnapshotItCannotRead(t *testing.T) {
	f := newHookFixture(t)
	cwd := t.TempDir()
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: cwd})
	bash := hookInput{SessionID: "c", Cwd: cwd, ToolName: "Bash", ToolUseID: "toolu_1"}
	f.run(t, "pre-tool-use", bash)
	snap := filepath.Join(f.state.root, "bash", "c-toolu_1.json")
	require.NoError(t, os.WriteFile(snap, []byte("not json"), 0o600))
	f.dirty = []string{filepath.Join(cwd, "a.go")}
	f.run(t, "tool-use", bash)

	require.Zero(t, f.api.called("Artifacts"), "a broken snapshot is no answer")
	_, err := os.Stat(snap)
	require.True(t, os.IsNotExist(err), "a broken snapshot is still removed")
}

func TestHookMemoryWalkMovesAFeedbackMemoryToGlobalOnce(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	writeMemory(t, dir, "prefers-pnpm", "Use pnpm", "feedback", "2026-09-17T10:00:00.000Z", "Always pnpm.")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	marker := f.state.LoadMarker(dir)
	e := marker["prefers-pnpm"]
	e.Scope = ""
	marker["prefers-pnpm"] = e
	require.NoError(t, f.state.SaveMarker(dir, marker), "a marker entry with no scope, which means the project")

	f.run(t, "session-start", hookInput{SessionID: "d", Cwd: "/repo"})
	old := f.notes(t, api.KindDecision, "prefers-pnpm")
	require.Len(t, old, 1, "the project copy gets one version that says where the memory went")
	require.Contains(t, old[0].Tags, api.TagRemoved)
	require.Contains(t, old[0].Body, "Moved to global on")
	require.NotEmpty(t, f.globalNotes(t, api.KindDecision, "prefers-pnpm"))
	require.Equal(t, api.GlobalProjectKey, f.state.LoadMarker(dir)["prefers-pnpm"].Scope)

	f.run(t, "session-start", hookInput{SessionID: "e", Cwd: "/repo"})
	require.Len(t, f.notes(t, api.KindDecision, "prefers-pnpm"), 1, "the move happens once")
}

func TestHookMemoryRemovalGoesWhereTheNoteLives(t *testing.T) {
	f := newHookFixture(t)
	dir := f.memoryDir(t)
	p := writeMemory(t, dir, "tone", "Answer plainly", "user", "2026-09-17T10:00:00.000Z", "plain words")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Len(t, f.globalNotes(t, api.KindNote, "tone"), 1)
	require.Empty(t, f.notes(t, api.KindNote, "tone"), "a user memory is not a project note")

	require.NoError(t, os.Remove(p))
	f.run(t, "session-start", hookInput{SessionID: "d", Cwd: "/repo"})
	versions := f.globalNotes(t, api.KindNote, "tone")
	require.Len(t, versions, 2)
	require.Contains(t, versions[1].Tags, api.TagRemoved)
	require.Empty(t, f.notes(t, api.KindNote, "tone"))
}

func TestHookKeysAFileInANestedWorktreeByThatWorktree(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "--quiet", "--initial-branch=main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README"), []byte("hi\n"), 0o644))
	git("add", "README")
	git("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "--quiet", "-m", "first")

	f := newHookFixture(t)
	f.r.gitOps.worktrees = gitinfo.Worktrees
	f.r.gitOps.status = gitinfo.Status
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: repo})
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: repo, Prompt: "add a changelog"})

	nested := filepath.Join(repo, ".claude", "worktrees", "changelog")
	bash := hookInput{SessionID: "c", Cwd: repo, ToolName: "Bash", ToolUseID: "toolu_1",
		ToolInput: json.RawMessage(`{"command":"git worktree add .claude/worktrees/changelog && echo x > .claude/worktrees/changelog/CHANGELOG"}`)}
	f.run(t, "pre-tool-use", bash)
	git("worktree", "add", "--quiet", "-b", "changelog", nested)
	require.NoError(t, os.WriteFile(filepath.Join(nested, "CHANGELOG"), []byte("x\n"), 0o644))
	f.run(t, "tool-use", bash)
	f.run(t, "tool-use", hookInput{SessionID: "c", Cwd: repo, ToolName: "Edit",
		ToolInput: json.RawMessage(`{"file_path":"` + filepath.Join(nested, "README") + `"}`)})

	require.Equal(t, [][]string{{"CHANGELOG"}, {"README"}}, f.session(t, "c").Artifacts,
		"a file inside a worktree under the checkout is keyed by that worktree, and the worktree folder is not a file")
}

const planFullNotice = "Bellek's free plan is full, so new tasks, notes and decisions are not saved."

func TestHookSessionStartSaysWhenTheFreePlanIsFull(t *testing.T) {
	f := newHookFixture(t)
	f.api.briefing.Notice = planFullNotice
	f.r.cfg.Recall = []string{"briefing"}
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"})
	out := f.out.String()
	require.Contains(t, out, planFullNotice)
	require.Contains(t, out, "Bellek briefing for session")
	require.Less(t, strings.Index(out, planFullNotice), strings.Index(out, "Bellek briefing"), "the line comes first")

	f.out.Reset()
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "resume"})
	require.Contains(t, f.out.String(), planFullNotice, "a live session reads the briefing and gets the line too")

	f.out.Reset()
	f.r.cfg.Recall = nil
	f.run(t, "session-start", hookInput{SessionID: "d", Cwd: "/repo", Source: "startup"})
	require.Contains(t, f.out.String(), planFullNotice, "with recall off a new session still hears that nothing is saved")
	require.NotContains(t, f.out.String(), "Bellek briefing")

	f.out.Reset()
	f.run(t, "session-start", hookInput{SessionID: "d", Cwd: "/repo", Source: "resume"})
	require.Empty(t, f.out.String(), "with recall off a live session makes no call and prints nothing")
	require.Empty(t, f.session(t, "d").Briefings)
}

func TestHookSessionStartPrintsNoPlanLineBelowTheCap(t *testing.T) {
	f := newHookFixture(t)
	f.r.cfg.Recall = []string{"briefing"}
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"})
	require.Contains(t, f.out.String(), "Bellek briefing for session")
	require.NotContains(t, f.out.String(), "free plan")
}

func TestHookSessionStartAtTheCapStaysQuietWhenTheBriefingFails(t *testing.T) {
	f := newHookFixture(t)
	f.api.briefing.Notice = planFullNotice
	f.recall("briefing")
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "startup"})
	f.out.Reset()
	f.api.down = true
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo", Source: "resume"})
	require.Empty(t, f.out.String(), "no briefing, no notice, and no half written answer")
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "briefing not read")
}
