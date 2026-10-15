package mcpbridge_test

import (
	"context"
	"testing"

	"github.com/bellek-si/bellek/internal/mcpbridge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type loggedBridge struct {
	agent  *mcp.ClientSession
	remote *mcp.ClientSession
	logs   *observer.ObservedLogs
}

func bridgedWithLog(t *testing.T) loggedBridge {
	t.Helper()
	core, logs := observer.New(zapcore.InfoLevel)
	log := zap.New(core).Sugar()
	remote := connect(t, newRemote(), "bridge")
	local, err := mcpbridge.New(context.Background(), remote, mcpbridge.Locals{Cwd: "/client/repo", RepoRoot: "/client/repo"}, "test", mcpbridge.WithLogger(log))
	require.NoError(t, err)
	return loggedBridge{agent: connect(t, local, "agent"), remote: remote, logs: logs}
}

func (b loggedBridge) calls() []map[string]any {
	out := []map[string]any{}
	for _, e := range b.logs.FilterMessage("mcp call").All() {
		out = append(out, e.ContextMap())
	}
	return out
}

func TestBridgeLogsForwardedCalls(t *testing.T) {
	b := bridgedWithLog(t)
	_, err := b.agent.CallTool(context.Background(), &mcp.CallToolParams{Name: "session_start", Arguments: map[string]any{"agentKind": "claudeCode", "agentId": "a"}})
	require.NoError(t, err)

	calls := b.calls()
	require.Len(t, calls, 1)
	require.Equal(t, "session_start", calls[0]["tool"])
	require.Contains(t, calls[0], "durationMs")
	require.NotContains(t, calls[0], "error")
}

func TestBridgeLogsTheErrorWhenTheToolFails(t *testing.T) {
	b := bridgedWithLog(t)
	ctx := context.Background()
	res, err := b.agent.CallTool(ctx, &mcp.CallToolParams{Name: "session_start", Arguments: map[string]any{"agentKind": "claudeCode", "agentId": "a"}})
	require.NoError(t, err)
	require.False(t, res.IsError)

	res, err = b.agent.CallTool(ctx, &mcp.CallToolParams{Name: "memory_get", Arguments: map[string]any{"key": "missing"}})
	require.NoError(t, err)
	require.True(t, res.IsError)

	calls := b.calls()
	require.Len(t, calls, 2)
	require.NotContains(t, calls[0], "error")
	require.Equal(t, "memory_get", calls[1]["tool"])
	require.Contains(t, calls[1]["error"], "notFound", "the bridge logs what failed, not just that something did")
}

func TestBridgeLogsTheErrorWhenTheRemoteIsGone(t *testing.T) {
	b := bridgedWithLog(t)
	require.NoError(t, b.remote.Close())

	_, err := b.agent.CallTool(context.Background(), &mcp.CallToolParams{Name: "task_next", Arguments: map[string]any{}})
	require.Error(t, err)

	calls := b.calls()
	require.Len(t, calls, 1)
	require.Equal(t, "task_next", calls[0]["tool"])
	require.Contains(t, calls[0], "durationMs")
	require.NotEmpty(t, calls[0]["error"])
}
