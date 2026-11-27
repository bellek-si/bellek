package main

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/client"
	"github.com/stretchr/testify/require"
)

const testRepoID = "6f1c2d1e-8a4b-4c3d-9e2f-1a2b3c4d5e6f"

type repoIDGit struct {
	remote    string
	stored    string
	minted    string
	newCalls  int
	mainRoots []string
}

func withoutRemote(f *hookFixture) *repoIDGit {
	g := &repoIDGit{minted: testRepoID, mainRoots: []string{"/home/jane/src/widgets", "/home/jane/src/widgets-feature"}}
	f.r.gitOps.remote = func(context.Context, string) string { return g.remote }
	f.r.gitOps.repoID = func(context.Context, string) string { return g.stored }
	f.r.gitOps.newRepoID = func(context.Context, string) string {
		g.newCalls++
		if g.stored == "" {
			g.stored = g.minted
		}
		return g.stored
	}
	f.r.gitOps.worktrees = func(context.Context, string) []string { return g.mainRoots }
	return g
}

func TestHookSessionStartWithoutAnAddressSendsTheRepoIDAndName(t *testing.T) {
	f := newHookFixture(t)
	g := withoutRemote(f)
	host, _ := os.Hostname()

	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/home/jane/src/widgets-feature"})
	s := f.session(t, "c")
	require.Equal(t, api.SessionStartInput{RepoID: testRepoID, RepoName: "widgets", AgentKind: "claudeCode", AgentID: host, Branch: "main"}, s.Start, "the name is the main worktree's folder, never a path")
	require.Equal(t, 1, g.newCalls)
	require.True(t, f.state.Addressless("c"))
	require.Empty(t, f.out.String())
	require.Empty(t, f.logs)
}

func TestHookSessionStartReusesAStoredRepoID(t *testing.T) {
	f := newHookFixture(t)
	g := withoutRemote(f)
	g.stored = "0b6d9a52-3f1e-4d8c-a7b2-5e4f3c2d1a0b"

	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.Equal(t, g.stored, f.session(t, "c").Start.RepoID)
	require.Zero(t, g.newCalls, "an id already in the config is not made again")
}

func TestHookSessionStartRedactsTheRepoName(t *testing.T) {
	f := newHookFixture(t)
	g := withoutRemote(f)
	g.mainRoots = []string{"/src/password=hunter2hunter2"}

	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/src/password=hunter2hunter2"})
	name := f.session(t, "c").Start.RepoName
	require.NotContains(t, name, "hunter2")
	require.Contains(t, name, "[redacted:")
}

func TestHookSessionStartWithAnAddressSendsNoName(t *testing.T) {
	f := newHookFixture(t)
	g := withoutRemote(f)
	g.remote = testRemote
	g.stored = testRepoID

	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	start := f.session(t, "c").Start
	require.Equal(t, testRemote, start.RepoRemote)
	require.Equal(t, testRepoID, start.RepoID, "a stored id still travels so the server can match a project made before the remote")
	require.Empty(t, start.RepoName)
	require.Zero(t, g.newCalls, "a repository with an address does not get a new id")
	require.False(t, f.state.Addressless("c"))
}

func TestHookSessionStartWithNeitherAddressNorIDRecordsNothing(t *testing.T) {
	f := newHookFixture(t)
	g := withoutRemote(f)
	g.minted = ""
	g.mainRoots = nil

	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/plain"})
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/plain", Prompt: "hello"})
	_, ok := f.state.Load("c")
	require.False(t, ok)
	require.False(t, f.state.Addressless("c"))
	require.Empty(t, f.out.String())
	require.Empty(t, f.logs)
	require.Empty(t, f.api.calls, "a folder with neither an address nor an id never reaches the server")
}

func TestHookSessionStartIsQuietWhenTheServerKnowsNoRepository(t *testing.T) {
	for _, recall := range [][]string{nil, {"briefing"}} {
		f := newHookFixture(t)
		f.r.cfg.Recall = recall
		withoutRemote(f)
		f.api.errs["SessionStart"] = &client.Error{Status: http.StatusBadRequest, Code: api.CodeNoRepository, Message: "no repository"}

		f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
		f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "hello"})
		_, ok := f.state.Load("c")
		require.False(t, ok, "recall %v", recall)
		require.False(t, f.state.Addressless("c"))
		require.Empty(t, f.out.String(), "an older server that does not know repoId tells the model nothing")
		require.Empty(t, f.logs, "an older server is not a failure")
		require.Equal(t, 1, f.api.called("SessionStart"))
	}
}

func TestHookPromptSendsTheRemoteOnceItAppears(t *testing.T) {
	f := newHookFixture(t)
	g := withoutRemote(f)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	s := f.session(t, "c")

	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "first"})
	require.Zero(t, f.api.called("SessionLocation"), "no remote yet, nothing to send")
	require.True(t, f.state.Addressless("c"))

	g.remote = "git@github.com:Acme/Widgets.git"
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "second"})
	require.Equal(t, []api.SessionLocationInput{{RepoRemote: "github.com/acme/widgets", RepoID: testRepoID, Branch: "main"}}, s.Locations)
	require.False(t, f.state.Addressless("c"))

	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "third"})
	require.Len(t, s.Locations, 1, "the address is sent once")
	require.Empty(t, f.logs)
}

func TestHookPromptRetriesTheRemoteAfterAFailure(t *testing.T) {
	f := newHookFixture(t)
	g := withoutRemote(f)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	s := f.session(t, "c")

	g.remote = testRemote
	f.api.errs["SessionLocation"] = errUnreachable
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "first"})
	require.Empty(t, s.Locations)
	require.True(t, f.state.Addressless("c"), "a failed send keeps the mark")
	require.Len(t, f.logs, 1)
	require.Contains(t, f.logs[0], "new remote not recorded")

	delete(f.api.errs, "SessionLocation")
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "second"})
	require.Len(t, s.Locations, 1)
	require.False(t, f.state.Addressless("c"))
}

func TestHookPromptNeverSendsALocationForASessionThatStartedWithAnAddress(t *testing.T) {
	f := newHookFixture(t)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	for _, prompt := range []string{"one", "two"} {
		f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: prompt})
	}
	require.Zero(t, f.api.called("SessionLocation"))
}

func TestHookPromptRestartWithAnAddressClearsTheMark(t *testing.T) {
	f := newHookFixture(t)
	g := withoutRemote(f)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	old, _ := f.state.Load("c")
	f.api.end(old)

	g.remote = testRemote
	f.run(t, "prompt", hookInput{SessionID: "c", Cwd: "/repo", Prompt: "keep going"})
	fresh := f.session(t, "c")
	require.NotEqual(t, old, fresh.ID)
	require.Equal(t, testRemote, fresh.Start.RepoRemote)
	require.False(t, f.state.Addressless("c"))
	require.Zero(t, f.api.called("SessionLocation"), "the new session already holds the address")
}

func TestHookCwdWithoutAnAddressSendsTheRepoID(t *testing.T) {
	f := newHookFixture(t)
	g := withoutRemote(f)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	s := f.session(t, "c")

	f.r.gitOps.branch = func(context.Context, string) string { return "dev" }
	f.run(t, "cwd", hookInput{SessionID: "c", Cwd: "/repo/api"})
	require.Equal(t, []api.SessionLocationInput{{RepoID: testRepoID, Branch: "dev"}}, s.Locations, "the id stands in for the missing address")
	require.Equal(t, 1, g.newCalls, "the cwd hook reads the id and never makes one")
	require.True(t, f.state.Addressless("c"))
	require.Empty(t, f.logs)
	require.Empty(t, f.out.String())
}

func TestHookStateAddresslessMark(t *testing.T) {
	s := newHookState(t.TempDir())
	require.False(t, s.Addressless("c"), "no mark before one is set")

	s.SetAddressless("c", true)
	require.True(t, s.Addressless("c"))
	require.DirExists(t, s.sessions(), "setting the mark makes the state directory")
	s.SetAddressless("c", true)
	require.True(t, s.Addressless("c"), "setting it twice is harmless")

	s.SetAddressless("c", false)
	require.False(t, s.Addressless("c"))
	s.SetAddressless("c", false)
	require.False(t, s.Addressless("c"), "clearing a missing mark is harmless")

	for _, bad := range []string{"", "a/b", "../escape"} {
		s.SetAddressless(bad, true)
		require.False(t, s.Addressless(bad), "an id that is not a plain name never has a mark: %q", bad)
	}
}

func TestHookStateDeleteClearsTheAddresslessMark(t *testing.T) {
	s := newHookState(t.TempDir())
	require.NoError(t, s.Save("c", [16]byte{1}))
	s.SetAddressless("c", true)
	s.SetAddressless("other", true)

	s.Delete("c")
	require.False(t, s.Addressless("c"))
	require.True(t, s.Addressless("other"), "another session keeps its mark")
}

func TestHookSessionEndClearsTheAddresslessMark(t *testing.T) {
	f := newHookFixture(t)
	withoutRemote(f)
	f.run(t, "session-start", hookInput{SessionID: "c", Cwd: "/repo"})
	require.True(t, f.state.Addressless("c"))

	f.run(t, "session-end", hookInput{SessionID: "c", Cwd: "/repo"})
	require.False(t, f.state.Addressless("c"))
}
