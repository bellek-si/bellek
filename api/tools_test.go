package api_test

import (
	"testing"

	"github.com/bellek-si/bellek/api"
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
