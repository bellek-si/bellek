package mcpbridge_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bellek-si/bellek/internal/mcpbridge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func object(properties ...string) map[string]any {
	props := map[string]any{}
	for _, p := range properties {
		props[p] = map[string]any{"type": "string"}
	}
	return map[string]any{"type": "object", "properties": props}
}

func echo(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: req.Params.Name + " done"},
		&mcp.TextContent{Text: string(req.Params.Arguments)},
	}}, nil
}

func refuse(text string) mcp.ToolHandler {
	return func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	}
}

func newRemote() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "remote", Version: "test"}, &mcp.ServerOptions{Instructions: "Search before you edit."})
	srv.AddTool(&mcp.Tool{Name: "session_start", Description: "Start a session.", InputSchema: object("agentKind", "agentId", "projectKey", "cwd", "repoRemote", "branch")}, echo)
	srv.AddTool(&mcp.Tool{Name: "memory_briefing", Description: "Read the briefing.", InputSchema: object("sessionId"),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, echo)
	srv.AddTool(&mcp.Tool{Name: "task_next", Description: "List the next tasks.", InputSchema: object("cwd", "repoRemote")}, echo)
	srv.AddTool(&mcp.Tool{Name: "task_status", Description: "Move a task.", InputSchema: object("sessionId", "key", "status")},
		refuse("invalidTransition: task t1 cannot move from todo to done"))
	srv.AddTool(&mcp.Tool{Name: "memory_get", Description: "Read a node.", InputSchema: object("key", "cwd")}, refuse("notFound: node missing not found"))
	return srv
}

func connect(t *testing.T, srv *mcp.Server, name string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	_, err := srv.Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: name, Version: "test"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func bridged(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	remote := connect(t, newRemote(), "bridge")
	local, err := mcpbridge.New(ctx, remote, mcpbridge.Locals{Cwd: "/client/repo", RepoRemote: "github.com/acme/bridged", Branch: "main"}, "test")
	require.NoError(t, err)
	return connect(t, local, "agent")
}

func payload(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	require.Len(t, res.Content, 2)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[1].(*mcp.TextContent).Text), &out))
	return out
}

func TestBridgeMirrorsToolsAndForwardsCalls(t *testing.T) {
	ctx := context.Background()
	remote := connect(t, newRemote(), "bridge")
	want, err := remote.ListTools(ctx, nil)
	require.NoError(t, err)
	local, err := mcpbridge.New(ctx, remote, mcpbridge.Locals{Cwd: "/client/repo", Branch: "main"}, "test")
	require.NoError(t, err)
	cs := connect(t, local, "agent")

	tools, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, want.Tools, tools.Tools, "the bridge lists whatever tools the server lists, with their schemas")
	for _, tl := range tools.Tools {
		if tl.Name == "memory_briefing" {
			require.True(t, tl.Annotations.ReadOnlyHint, "the hints the server gives reach the agent")
		}
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "session_start", Arguments: map[string]any{"agentKind": "claudeCode", "agentId": "a"}})
	require.NoError(t, err)
	require.False(t, res.IsError, res.Content[0].(*mcp.TextContent).Text)
	require.Equal(t, "session_start done", res.Content[0].(*mcp.TextContent).Text, "the answer comes back as the server gave it")
	require.Equal(t, map[string]any{"agentKind": "claudeCode", "agentId": "a", "cwd": "/client/repo", "branch": "main"}, payload(t, res))

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "task_status", Arguments: map[string]any{"sessionId": "s", "key": "t1", "status": "done"}})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content[0].(*mcp.TextContent).Text, "invalidTransition")
}

func TestBridgeSendsTheRepoRemoteOfItsCheckout(t *testing.T) {
	cs := bridged(t)
	ctx := context.Background()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "session_start", Arguments: map[string]any{"agentKind": "codex"}})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Equal(t, "github.com/acme/bridged", payload(t, res)["repoRemote"], "the server names the project after the remote of the client's checkout")

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "session_start", Arguments: map[string]any{"agentKind": "codex", "projectKey": "bridged"}})
	require.NoError(t, err)
	require.False(t, res.IsError, res.Content[0].(*mcp.TextContent).Text)
	got := payload(t, res)
	require.Equal(t, "bridged", got["projectKey"])
	require.Equal(t, "github.com/acme/bridged", got["repoRemote"])
}

func TestFillOnlyAddsMissingKnownKeys(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"cwd": map[string]any{}, "branch": map[string]any{}, "sessionId": map[string]any{}}}
	locals := mcpbridge.Locals{Cwd: "/here", RepoRemote: "git@x", Branch: "dev"}
	args, err := mcpbridge.Fill(schema, json.RawMessage(`{"cwd":"/given"}`), locals)
	require.NoError(t, err)
	require.Equal(t, "/given", args["cwd"])
	_, hasBranch := args["branch"]
	require.False(t, hasBranch, "the bridge's branch belongs to its own cwd, not the one the caller named")
	_, hasRemote := args["repoRemote"]
	require.False(t, hasRemote)
	args, err = mcpbridge.Fill(schema, nil, locals)
	require.NoError(t, err)
	require.Equal(t, "/here", args["cwd"])
	_, err = mcpbridge.Fill(schema, json.RawMessage(`{bad`), locals)
	require.Error(t, err)
}

func TestBridgePassesOnTheInstructionsOfTheServer(t *testing.T) {
	ctx := context.Background()
	remoteSrv := mcp.NewServer(&mcp.Implementation{Name: "remote", Version: "test"}, &mcp.ServerOptions{Instructions: "Search before you edit."})
	remote := connect(t, remoteSrv, "bridge")

	local, err := mcpbridge.New(ctx, remote, mcpbridge.Locals{}, "test")
	require.NoError(t, err)
	cs := connect(t, local, "agent")
	require.Equal(t, "Search before you edit.", cs.InitializeResult().Instructions)
}
