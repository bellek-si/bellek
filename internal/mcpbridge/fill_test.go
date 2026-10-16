package mcpbridge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bellek-si/bellek/internal/mcpbridge"
	"github.com/stretchr/testify/require"
)

type property struct {
	Type string `json:"type"`
}

type objectSchema struct {
	Type       string              `json:"type"`
	Properties map[string]property `json:"properties"`
}

var locals = mcpbridge.Locals{Cwd: "/here", RepoRemote: "github.com/acme/widgets", Branch: "dev"}

func TestFillReadsSchemasThatAreNotMaps(t *testing.T) {
	tests := []struct {
		name       string
		schema     any
		wantFilled bool
	}{
		{"struct value", objectSchema{Type: "object", Properties: map[string]property{"cwd": {Type: "string"}}}, true},
		{"struct pointer", &objectSchema{Type: "object", Properties: map[string]property{"cwd": {Type: "string"}}}, true},
		{"struct without the key", objectSchema{Type: "object", Properties: map[string]property{"limit": {Type: "integer"}}}, false},
		{"schema that is not an object", "cwd", false},
		{"schema that cannot be marshalled", make(chan int), false},
		{"nil schema", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := mcpbridge.Fill(tt.schema, nil, locals)
			require.NoError(t, err)
			if tt.wantFilled {
				require.Equal(t, "/here", args["cwd"])
			} else {
				require.Empty(t, args)
			}
		})
	}
}

func TestFillOverwritesEmptyStringsAndKeepsExtras(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"cwd": map[string]any{}, "branch": map[string]any{}, "repoRemote": map[string]any{},
	}}
	args, err := mcpbridge.Fill(schema, json.RawMessage(`{"cwd":"","note":"keep me"}`), locals)
	require.NoError(t, err)
	require.Equal(t, "/here", args["cwd"], "an empty string is not an answer, the local value wins")
	require.Equal(t, "dev", args["branch"])
	require.Equal(t, locals.RepoRemote, args["repoRemote"])
	require.Equal(t, "keep me", args["note"], "arguments the schema does not mention are forwarded untouched")
}

func TestFillTreatsAnExplicitNullAsMissing(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"branch": map[string]any{}}}
	args, err := mcpbridge.Fill(schema, json.RawMessage(`{"branch":null}`), locals)
	require.NoError(t, err)
	require.Equal(t, locals.Branch, args["branch"], "a null is not an answer, so the local branch is substituted")
}

func TestFillSkipsEmptyLocals(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"cwd": map[string]any{}, "branch": map[string]any{}}}
	args, err := mcpbridge.Fill(schema, nil, mcpbridge.Locals{Cwd: "/here"})
	require.NoError(t, err)
	require.Equal(t, "/here", args["cwd"])
	require.NotContains(t, args, "branch")
}

func TestFillAddsTheLocalsEachToolTakesAndNothingElse(t *testing.T) {
	all := mcpbridge.Locals{Cwd: "/here", RepoRemote: "github.com/acme/widgets", Branch: "dev"}
	tests := []struct {
		name   string
		schema map[string]any
		want   map[string]any
	}{
		{
			"session_start, without the agentKind only the caller knows",
			object("agentKind", "agentId", "projectKey", "cwd", "repoRemote", "branch", "maxTokens", "harnessSessionId"),
			map[string]any{"cwd": all.Cwd, "repoRemote": all.RepoRemote, "branch": all.Branch},
		},
		{
			"memory_get, which takes no branch",
			object("key", "kind", "depth", "asOf", "cwd", "projectKey", "repoRemote"),
			map[string]any{"cwd": all.Cwd, "repoRemote": all.RepoRemote},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := mcpbridge.Fill(tt.schema, nil, all)
			require.NoError(t, err)
			require.Equal(t, tt.want, args)
		})
	}
}

func TestFillTakesNullArgumentsAsNone(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"cwd": map[string]any{}}}
	args, err := mcpbridge.Fill(schema, json.RawMessage("null"), locals)
	require.NoError(t, err)
	require.Equal(t, locals.Cwd, args["cwd"])
}

func TestNewFailsWhenTheRemoteIsClosed(t *testing.T) {
	remote := connect(t, newRemote(), "bridge")
	require.NoError(t, remote.Close())

	srv, err := mcpbridge.New(context.Background(), remote, locals, "test")
	require.Nil(t, srv)
	require.ErrorContains(t, err, "list remote tools")
}

func TestConnectFailsOnAnUnreachableServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := mcpbridge.Connect(ctx, url, "token", "test")
	require.Error(t, err, "nothing is listening on that port any more")
	require.Nil(t, session)
}

func TestConnectFailsWhenTheServerRejectsTheToken(t *testing.T) {
	seen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.Header.Get("Authorization"):
		default:
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := mcpbridge.Connect(ctx, srv.URL, "secret-token", "test")
	require.Error(t, err)
	require.Equal(t, "Bearer secret-token", <-seen, "the bridge sends the bearer token")
}

func TestLocalsFromReadsTheGitRepository(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "trunk"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"remote", "add", "origin", "https://example.com/acme/Widgets.git"},
		{"commit", "--allow-empty", "-m", "first"},
	} {
		require.NoError(t, exec.Command("git", append([]string{"-C", dir}, args...)...).Run(), args)
	}

	got := mcpbridge.LocalsFrom(dir)
	require.Equal(t, dir, got.Cwd)
	require.Equal(t, "example.com/acme/widgets", got.RepoRemote)
	require.Equal(t, "trunk", got.Branch)

	sub := filepath.Join(dir, "cmd")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.Equal(t, got.RepoRemote, mcpbridge.LocalsFrom(sub).RepoRemote, "a directory inside the repository has the same address")
}

func TestLocalsFromOutsideAGitRepository(t *testing.T) {
	dir := t.TempDir()
	got := mcpbridge.LocalsFrom(dir)
	require.Equal(t, dir, got.Cwd)
	require.Empty(t, got.RepoRemote)
	require.Empty(t, got.Branch)

	empty := mcpbridge.LocalsFrom("")
	require.Equal(t, mcpbridge.Locals{}, empty)
}

func TestFillReadsTheGitStateOfTheCallersOwnCwd(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "feature"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"remote", "add", "origin", "https://example.com/acme/other.git"},
		{"commit", "--allow-empty", "-m", "first"},
	} {
		require.NoError(t, exec.Command("git", append([]string{"-C", dir}, args...)...).Run(), args)
	}
	schema := map[string]any{"type": "object", "properties": map[string]any{"cwd": map[string]any{}, "repoRemote": map[string]any{}, "branch": map[string]any{}}}

	args, err := mcpbridge.Fill(schema, json.RawMessage(`{"cwd":"`+dir+`"}`), locals)
	require.NoError(t, err)
	require.Equal(t, "example.com/acme/other", args["repoRemote"], "not the remote of the bridge's own checkout")
	require.Equal(t, "feature", args["branch"])

	args, err = mcpbridge.Fill(schema, json.RawMessage(`{"cwd":"`+dir+`","repoRemote":"git@github.com:acme/given.git"}`), locals)
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/given", args["repoRemote"], "a value the caller sent still wins")
}

func TestFillStripsCredentialsFromARemoteTheCallerSent(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"repoRemote": map[string]any{}}}
	args, err := mcpbridge.Fill(schema, json.RawMessage(`{"repoRemote":"https://x-access-token:ghs_secret@github.com/acme/app.git"}`), locals)
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/app", args["repoRemote"])
}

func TestLocalsAreRedactedLikeTheHookLocation(t *testing.T) {
	l := mcpbridge.LocalsFrom(filepath.Join(t.TempDir(), "password=hunter2hunter2"))
	require.NotContains(t, l.Cwd, "hunter2")
	require.Contains(t, l.Cwd, "[redacted:")
}

func TestFillKeepsTheBridgesGitStateWhenTheCallerNamesNoOtherCwd(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"cwd": map[string]any{}, "repoRemote": map[string]any{}, "branch": map[string]any{}}}
	for _, raw := range []string{`{"cwd":"/here"}`, `{"cwd":""}`, `{"cwd":7}`, `{}`} {
		t.Run(raw, func(t *testing.T) {
			args, err := mcpbridge.Fill(schema, json.RawMessage(raw), locals)
			require.NoError(t, err)
			require.Equal(t, locals.RepoRemote, args["repoRemote"], "/here is no repository, so a fresh lookup would find no remote")
			require.Equal(t, "dev", args["branch"])
		})
	}
}

func TestTheKeyGoesToTheServerHostOnly(t *testing.T) {
	var elsewhere string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere = r.Header.Get("Authorization")
	}))
	t.Cleanup(other.Close)
	var own string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		own = r.Header.Get("Authorization")
		http.Redirect(w, r, other.URL+"/mcp", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)

	res, err := mcpbridge.HTTPClient(server.URL, "bk_secret").Get(server.URL + "/mcp")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, "Bearer bk_secret", own)
	require.Empty(t, elsewhere, "a redirect to another host does not carry the key")
}
