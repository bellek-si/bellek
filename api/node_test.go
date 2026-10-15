package api_test

import (
	"encoding/json"
	"testing"

	"github.com/bellek-si/bellek/api"
	"github.com/stretchr/testify/require"
)

func TestKindValidAcceptsTheSevenKindsOnly(t *testing.T) {
	tests := []struct {
		kind api.Kind
		want bool
	}{
		{api.KindProject, true},
		{api.KindTask, true},
		{api.KindSession, true},
		{api.KindAgent, true},
		{api.KindNote, true},
		{api.KindDecision, true},
		{api.KindArtifact, true},
		{"", false},
		{"Task", false},
		{"tasks", false},
		{"../agent", false},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, tt.kind.Valid(), "%q", tt.kind)
	}
}

func TestOnlyNotesAndDecisionsMayBePrivate(t *testing.T) {
	require.True(t, api.KindNote.Private())
	require.True(t, api.KindDecision.Private())
	for _, k := range []api.Kind{api.KindProject, api.KindTask, api.KindSession, api.KindAgent, api.KindArtifact, "", "notes"} {
		require.False(t, k.Private(), "%q", k)
	}
}

func TestEveryEnumAcceptsItsOwnValuesOnly(t *testing.T) {
	tests := []struct {
		name  string
		valid func(string) bool
		want  []string
	}{
		{"origin", func(s string) bool { return api.Origin(s).Valid() }, []string{"user", "agent", "hook", "import"}},
		{"visibility", func(s string) bool { return api.Visibility(s).Valid() }, []string{"private", "shared"}},
		{"task status", func(s string) bool { return api.TaskStatus(s).Valid() }, []string{"todo", "inProgress", "blocked", "review", "done", "cancelled"}},
		{"session status", func(s string) bool { return api.SessionStatus(s).Valid() }, []string{"active", "ended"}},
		{"edge type", func(s string) bool { return api.EdgeType(s).Valid() }, []string{"belongsTo", "parentOf", "dependsOn", "blockedBy", "workedOnBy", "produced", "relatesTo", "supersedes"}},
		{"event type", func(s string) bool { return api.EventType(s).Valid() }, []string{"statusChanged", "claimed", "released", "handoff", "signal", "noteAdded", "progress", "prompt"}},
		{"role", func(s string) bool { return api.Role(s).Valid() }, []string{"admin", "member"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, v := range tt.want {
				require.True(t, tt.valid(v), "%q", v)
			}
			for _, v := range []string{"", "Done", "inprogress", "unknown", tt.want[0] + " "} {
				require.False(t, tt.valid(v), "%q", v)
			}
		})
	}
}

func TestNodeEdgeAndEventKeepTheirEnumsAsPlainStringsInJSON(t *testing.T) {
	tests := []struct {
		name string
		wire string
		into any
		want any
	}{
		{"node", `{"origin":"agent","visibility":"shared"}`, &api.Node{}, &api.Node{Origin: api.OriginAgent, Visibility: api.VisibilityShared}},
		{"edge", `{"type":"dependsOn"}`, &api.Edge{}, &api.Edge{Type: api.EdgeDependsOn}},
		{"event", `{"type":"claimed","origin":"hook"}`, &api.Event{}, &api.Event{Type: api.EventClaimed, Origin: api.OriginHook}},
		{"node with values this client does not know", `{"origin":"sync","visibility":"team"}`, &api.Node{}, &api.Node{Origin: "sync", Visibility: "team"}},
		{"edge with a type this client does not know", `{"type":"mentions"}`, &api.Edge{}, &api.Edge{Type: "mentions"}},
		{"event with values this client does not know", `{"type":"reviewed","origin":"sync"}`, &api.Event{}, &api.Event{Type: "reviewed", Origin: "sync"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, json.Unmarshal([]byte(tt.wire), tt.into))
			require.Equal(t, tt.want, tt.into)

			raw, err := json.Marshal(tt.want)
			require.NoError(t, err)
			var sent, wire map[string]any
			require.NoError(t, json.Unmarshal(raw, &sent))
			require.NoError(t, json.Unmarshal([]byte(tt.wire), &wire))
			for key, value := range wire {
				require.Equal(t, value, sent[key], key)
			}
		})
	}
}

func TestLabelPutsTheTaskIDBeforeTheKey(t *testing.T) {
	require.Equal(t, "auth-refresh", api.Node{Key: "auth-refresh"}.Label())
	require.Equal(t, "widgets-7 auth-refresh", api.Node{Key: "auth-refresh", Number: 7, Ref: "widgets-7"}.Label())
	require.Equal(t, "auth-refresh", api.Ref{Key: "auth-refresh"}.Label())
	require.Equal(t, "widgets-7 auth-refresh", api.Ref{Key: "auth-refresh", Ref: "widgets-7"}.Label())
}
