package mcpbridge_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/internal/mcpbridge"
	"github.com/stretchr/testify/require"
)

var noRemote = mcpbridge.Locals{RepoID: "6f1c2d1e-8a4b-4c3d-9e2f-1a2b3c4d5e6f", RepoName: "widgets", Branch: "dev"}

func TestFillAddsTheRepoIDAndNameOnlyWhereTheSchemaListsThem(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
		want   map[string]any
	}{
		{
			"session_start",
			object("agentKind", "projectKey", "repoRemote", "repoId", "repoName", "branch"),
			map[string]any{"repoId": noRemote.RepoID, "repoName": "widgets", "branch": "dev"},
		},
		{
			"memory_get, which takes an id but no name",
			object("key", "projectKey", "repoRemote", "repoId"),
			map[string]any{"repoId": noRemote.RepoID},
		},
		{
			"a tool of an older server, which knows neither",
			object("key", "projectKey", "repoRemote"),
			map[string]any{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := mcpbridge.Fill(tt.schema, nil, noRemote)
			require.NoError(t, err)
			require.Equal(t, tt.want, args)
		})
	}
}

func TestFillLeavesTheRepoIDOutWhenTheCallerNamesAnotherProject(t *testing.T) {
	schema := object("projectKey", "repoRemote", "repoId", "repoName", "branch")
	for _, raw := range []string{
		`{"projectKey":"other"}`,
		`{"repoRemote":"github.com/acme/other"}`,
		`{"projectKey":"other","repoRemote":"github.com/acme/other"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			args, err := mcpbridge.Fill(schema, json.RawMessage(raw), noRemote)
			require.NoError(t, err)
			require.NotContains(t, args, "repoId", "this folder's id must not travel with another project")
			require.NotContains(t, args, "repoName")
			require.Equal(t, "dev", args["branch"], "the branch is still filled")
		})
	}
}

func TestFillAddsTheRepoIDWhenTheCallerSentAnEmptyScope(t *testing.T) {
	schema := object("projectKey", "repoRemote", "repoId", "repoName")
	for _, raw := range []string{`{"projectKey":""}`, `{"repoRemote":null}`} {
		t.Run(raw, func(t *testing.T) {
			args, err := mcpbridge.Fill(schema, json.RawMessage(raw), noRemote)
			require.NoError(t, err)
			require.Equal(t, noRemote.RepoID, args["repoId"], "an empty value names no other project")
			require.Equal(t, "widgets", args["repoName"])
		})
	}
}

func TestFillKeepsARepoIDTheCallerSent(t *testing.T) {
	args, err := mcpbridge.Fill(object("repoId", "repoName"), json.RawMessage(`{"repoId":"mine","repoName":"named"}`), noRemote)
	require.NoError(t, err)
	require.Equal(t, "mine", args["repoId"])
	require.Equal(t, "named", args["repoName"])
}

func TestFillSendsTheRepoIDBesideTheLocalRemote(t *testing.T) {
	both := mcpbridge.Locals{RepoRemote: "github.com/acme/widgets", RepoID: noRemote.RepoID, Branch: "dev"}
	args, err := mcpbridge.Fill(object("repoRemote", "repoId", "repoName"), nil, both)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"repoRemote": both.RepoRemote, "repoId": both.RepoID}, args, "a local remote is this folder, so its id may travel with it")
}

func TestLocalsFromARepositoryWithoutARemote(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "widgets")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for _, args := range [][]string{
		{"init", "-b", "trunk"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-m", "first"},
	} {
		require.NoError(t, exec.Command("git", append([]string{"-C", dir}, args...)...).Run(), args)
	}

	got := mcpbridge.LocalsFrom(dir)
	require.Empty(t, got.RepoRemote)
	id, ok := api.CanonicalUUID(got.RepoID)
	require.True(t, ok, "the id is a uuid: %q", got.RepoID)
	require.Equal(t, id, got.RepoID)
	require.Equal(t, "widgets", got.RepoName)
	require.Equal(t, "trunk", got.Branch)

	sub := filepath.Join(dir, "cmd")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	again := mcpbridge.LocalsFrom(sub)
	require.Equal(t, got.RepoID, again.RepoID, "the id is made once and read again")
	require.Equal(t, "widgets", again.RepoName)

	require.NoError(t, exec.Command("git", "-C", dir, "remote", "add", "origin", "https://example.com/acme/widgets.git").Run())
	withRemote := mcpbridge.LocalsFrom(dir)
	require.Equal(t, "example.com/acme/widgets", withRemote.RepoRemote)
	require.Equal(t, got.RepoID, withRemote.RepoID, "the id stays once a remote appears")
	require.Empty(t, withRemote.RepoName, "a repository with a remote needs no name")
}

func TestLocalsFromARepositoryWithARemoteMakesNoID(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "trunk"},
		{"remote", "add", "origin", "https://example.com/acme/widgets.git"},
	} {
		require.NoError(t, exec.Command("git", append([]string{"-C", dir}, args...)...).Run(), args)
	}
	got := mcpbridge.LocalsFrom(dir)
	require.Empty(t, got.RepoID)
	out, _ := exec.Command("git", "-C", dir, "config", "--local", "--get", "bellek.repoId").Output()
	require.Empty(t, out, "the bridge writes nothing into a repository that has a remote")
}
