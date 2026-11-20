package api_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bellek-si/bellek/api"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestThereAreTwelveToolsWithDistinctNames(t *testing.T) {
	require.Len(t, api.Tools, 12, "adding a tool means removing one")
	names := map[string]bool{}
	for _, tool := range api.Tools {
		require.NotEmpty(t, tool.Description, tool.Name)
		require.False(t, names[tool.Name], "%s is listed twice", tool.Name)
		names[tool.Name] = true
	}
}

func TestEveryArgsWithASessionIDReturnsItFromSession(t *testing.T) {
	s := api.SessionArgs{SessionID: "s1"}
	for _, args := range []interface{ Session() string }{
		api.BriefingArgs{SessionArgs: s},
		api.NoteArgs{SessionArgs: s},
		api.SignalArgs{SessionArgs: s},
		api.TaskUpsertArgs{SessionArgs: s},
		api.TaskStatusArgs{SessionArgs: s},
		api.TaskClaimArgs{SessionArgs: s},
		api.TaskHandoffArgs{SessionArgs: s},
		api.SessionEndArgs{SessionArgs: s},
	} {
		require.Equal(t, "s1", args.Session(), "%T", args)
	}
}

func TestMemoryGetAndMemorySearchTakeAnOptionalSessionID(t *testing.T) {
	ctx := context.Background()
	srv := mcp.NewServer(&mcp.Implementation{Name: "bellek", Version: "test"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: api.ToolGet.Name}, func(context.Context, *mcp.CallToolRequest, api.GetArgs) (*mcp.CallToolResult, any, error) {
		return nil, nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: api.ToolSearch.Name}, func(context.Context, *mcp.CallToolRequest, api.SearchArgs) (*mcp.CallToolResult, any, error) {
		return nil, nil, nil
	})
	st, ct := mcp.NewInMemoryTransports()
	_, err := srv.Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "test"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	tools, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 2)
	for _, tool := range tools.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var schema struct {
			Properties map[string]map[string]any `json:"properties"`
			Required   []string                  `json:"required"`
		}
		require.NoError(t, json.Unmarshal(raw, &schema))
		require.Contains(t, schema.Properties, "sessionId", tool.Name)
		require.Equal(t, "string", schema.Properties["sessionId"]["type"], tool.Name)
		require.NotContains(t, schema.Required, "sessionId", "%s works without a session", tool.Name)
	}
}
