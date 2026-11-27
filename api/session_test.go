package api_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bellek-si/bellek/api"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestTheRepoIDFieldsAreCamelCaseAndLeftOutWhenEmpty(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"start with an id", api.SessionStartInput{RepoID: "6f1c2d1e-8a4b-4c3d-9e2f-1a2b3c4d5e6f", RepoName: "widgets", AgentKind: "claudeCode"},
			`{"repoId":"6f1c2d1e-8a4b-4c3d-9e2f-1a2b3c4d5e6f","repoName":"widgets","agentKind":"claudeCode"}`},
		{"start without an id", api.SessionStartInput{RepoRemote: "github.com/acme/widgets", AgentKind: "claudeCode"},
			`{"repoRemote":"github.com/acme/widgets","agentKind":"claudeCode"}`},
		{"location with an id", api.SessionLocationInput{RepoRemote: "github.com/acme/widgets", RepoID: "6f1c2d1e-8a4b-4c3d-9e2f-1a2b3c4d5e6f"},
			`{"repoRemote":"github.com/acme/widgets","repoId":"6f1c2d1e-8a4b-4c3d-9e2f-1a2b3c4d5e6f"}`},
		{"location without an id", api.SessionLocationInput{RepoRemote: "github.com/acme/widgets"},
			`{"repoRemote":"github.com/acme/widgets"}`},
		{"tool scope", api.GetArgs{ScopeArgs: api.ScopeArgs{RepoID: "x"}, Key: "k"}, `{"repoId":"x","key":"k"}`},
		{"tool session start", api.SessionStartArgs{ScopeArgs: api.ScopeArgs{RepoID: "x"}, RepoName: "widgets", AgentKind: "claudeCode"},
			`{"repoId":"x","repoName":"widgets","agentKind":"claudeCode"}`},
		{"tool session start without an id", api.SessionStartArgs{AgentKind: "claudeCode"}, `{"agentKind":"claudeCode"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.value)
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(raw))
		})
	}
}

func TestOnlySessionStartTakesARepoNameAndEveryScopedToolTakesARepoID(t *testing.T) {
	ctx := context.Background()
	srv := mcp.NewServer(&mcp.Implementation{Name: "bellek", Version: "test"}, nil)
	addTool[api.SessionStartArgs](srv, api.ToolSessionStart.Name)
	addTool[api.GetArgs](srv, api.ToolGet.Name)
	addTool[api.SearchArgs](srv, api.ToolSearch.Name)
	addTool[api.TaskNextArgs](srv, api.ToolTaskNext.Name)

	st, ct := mcp.NewInMemoryTransports()
	_, err := srv.Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "test"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	tools, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 4)
	for _, tool := range tools.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var schema struct {
			Properties map[string]map[string]any `json:"properties"`
			Required   []string                  `json:"required"`
		}
		require.NoError(t, json.Unmarshal(raw, &schema))
		require.Contains(t, schema.Properties, "repoId", tool.Name)
		require.NotContains(t, schema.Required, "repoId", tool.Name)
		if tool.Name == api.ToolSessionStart.Name {
			require.Contains(t, schema.Properties, "repoName")
			require.NotContains(t, schema.Required, "repoName")
		} else {
			require.NotContains(t, schema.Properties, "repoName", "%s does not create a project, so it needs no name", tool.Name)
		}
	}
}

func addTool[In any](srv *mcp.Server, name string) {
	mcp.AddTool(srv, &mcp.Tool{Name: name}, func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, any, error) {
		return nil, nil, nil
	})
}
