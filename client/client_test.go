package client_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/client"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type request struct {
	method, uri, auth, contentType, body string
}

// recording serves every request with one status and one body, and keeps
// the last request.
func recording(t *testing.T, status int, answer string) (*client.Client, *request) {
	t.Helper()
	got := &request{}
	c := serving(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		*got = request{method: r.Method, uri: r.URL.RequestURI(), auth: r.Header.Get("Authorization"), contentType: r.Header.Get("Content-Type"), body: string(raw)}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	})
	return c, got
}

func TestEveryCallSendsItsMethodPathAndBody(t *testing.T) {
	ctx := context.Background()
	id := uuid.MustParse("01999b1a-0000-7000-8000-000000000001")
	sid := id.String()
	tests := []struct {
		name   string
		status int
		answer string
		call   func(t *testing.T, c *client.Client)
		method string
		uri    string
		body   string
	}{
		{
			"health", http.StatusOK, `{"status":"ok","version":"v1.3.0","minClientVersion":"v0.1.0"}`,
			func(t *testing.T, c *client.Client) {
				h, err := c.Health(ctx)
				require.NoError(t, err)
				require.Equal(t, &api.Health{Status: "ok", Version: "v1.3.0", MinClientVersion: "v0.1.0"}, h)
			},
			http.MethodGet, "/v1/health", "",
		},
		{
			"session start", http.StatusCreated, `{"session":{"id":"` + sid + `","key":"session-000000000001","status":"active","props":{"branch":"main"}},"briefing":{"markdown":"## Frontier\n","truncated":true}}`,
			func(t *testing.T, c *client.Client) {
				started, err := c.SessionStart(ctx, api.SessionStartInput{Cwd: "/client/repo", RepoRemote: "git@github.com:acme/widgets.git", RepoRoot: "/client/repo", AgentKind: "claudeCode", AgentID: "a", Branch: "main", MaxTokens: 800, HarnessSessionID: "c0ffee00-1111-2222-3333-444455556666"})
				require.NoError(t, err)
				require.Equal(t, id, started.Session.ID)
				require.Equal(t, "session-000000000001", started.Session.Key)
				require.Equal(t, "main", started.Session.Props["branch"])
				require.Equal(t, api.Briefing{Markdown: "## Frontier\n", Truncated: true}, started.Briefing)
			},
			http.MethodPost, "/v1/sessions",
			`{"cwd":"/client/repo","repoRemote":"git@github.com:acme/widgets.git","repoRoot":"/client/repo","agentKind":"claudeCode","agentId":"a","branch":"main","maxTokens":800,"harnessSessionId":"c0ffee00-1111-2222-3333-444455556666"}`,
		},
		{
			"prompt", http.StatusCreated, `{"turn":2,"previousTurnFiles":1,"previousTurnNoted":false,"related":[],"signals":[],"focus":{"kind":"task","key":"auth-refresh","status":"inProgress"},"focusTurn":3}`,
			func(t *testing.T, c *client.Client) {
				res, err := c.Prompt(ctx, id, "fix internal/auth.go", nil)
				require.NoError(t, err)
				require.Equal(t, 2, res.Turn)
				require.Equal(t, 1, res.PreviousTurnFiles)
				require.False(t, res.PreviousTurnNoted)
				require.Equal(t, &api.Ref{Kind: api.KindTask, Key: "auth-refresh", Status: "inProgress"}, res.Focus)
				require.Equal(t, 3, res.FocusTurn)
			},
			http.MethodPost, "/v1/sessions/" + sid + "/prompt", `{"message":"fix internal/auth.go"}`,
		},
		{
			"artifacts", http.StatusCreated, `{"items":[{"key":"internal/auth.go","title":"internal/auth.go","links":[],"sessions":[]}]}`,
			func(t *testing.T, c *client.Client) {
				items, err := c.Artifacts(ctx, id, []string{"internal/auth.go"})
				require.NoError(t, err)
				require.Len(t, items, 1)
				require.Equal(t, "internal/auth.go", items[0].Key)
				require.Empty(t, items[0].Links)
			},
			http.MethodPost, "/v1/sessions/" + sid + "/artifacts", `{"paths":["internal/auth.go"]}`,
		},
		{
			"progress", http.StatusCreated, `{"id":"01999b1a-0000-7000-8000-000000000002","type":"progress","message":"rotated the token"}`,
			func(t *testing.T, c *client.Client) {
				require.NoError(t, c.Progress(ctx, id, api.ProgressInput{Message: "rotated the token", Name: "rotate-the-token"}))
			},
			http.MethodPost, "/v1/sessions/" + sid + "/progress", `{"message":"rotated the token","name":"rotate-the-token"}`,
		},
		{
			"briefing", http.StatusOK, `{"markdown":"## This session\n- files: internal/auth.go\n","truncated":false}`,
			func(t *testing.T, c *client.Client) {
				b, err := c.Briefing(ctx, id, 100)
				require.NoError(t, err)
				require.Equal(t, "## This session\n- files: internal/auth.go\n", b.Markdown)
			},
			http.MethodGet, "/v1/briefing?sessionId=" + sid + "&maxTokens=100", "",
		},
		{
			"note", http.StatusCreated, `{"id":"01999b1a-0000-7000-8000-000000000003","kind":"note","key":"finding","title":"Finding","version":1}`,
			func(t *testing.T, c *client.Client) {
				note, err := c.Note(ctx, api.NoteInput{SessionID: id, Title: "Finding", Body: "ErrTokenExpired in internal/auth.go", Tags: []string{"lesson"}})
				require.NoError(t, err)
				require.Equal(t, api.KindNote, note.Kind)
				require.Equal(t, "finding", note.Key)
			},
			http.MethodPost, "/v1/notes", `{"sessionId":"` + sid + `","title":"Finding","body":"ErrTokenExpired in internal/auth.go","tags":["lesson"]}`,
		},
		{
			"import", http.StatusOK, `{"created":2,"updated":0,"unchanged":1,"links":1,"skipped":0}`,
			func(t *testing.T, c *client.Client) {
				rep, err := c.Import(ctx, "bellek", []api.ImportDoc{
					{Kind: "task", Key: "t1", Title: "T1", Status: "done"},
					{Kind: "task", Key: "t2", Title: "T2", Links: []api.ImportLink{{Type: "dependsOn", To: "t1"}}},
				})
				require.NoError(t, err)
				require.Equal(t, &api.ImportReport{Created: 2, Unchanged: 1, Links: 1}, rep)
			},
			http.MethodPost, "/v1/projects/bellek/import",
			`{"docs":[{"kind":"task","key":"t1","title":"T1","status":"done"},{"kind":"task","key":"t2","title":"T2","links":[{"type":"dependsOn","to":"t1"}]}]}`,
		},
		{
			"export", http.StatusOK, `{"project":{"kind":"project","key":"bellek"},"nodes":[{"kind":"task","key":"t1"}],"edges":{},"events":{},"targets":{}}`,
			func(t *testing.T, c *client.Client) {
				snap, err := c.Export(ctx, "bellek", nil)
				require.NoError(t, err)
				require.Equal(t, "bellek", snap.Project.Key)
				require.Len(t, snap.Nodes, 1)
				require.Nil(t, snap.Next)
			},
			http.MethodGet, "/v1/projects/bellek/export", "",
		},
		{
			"report", http.StatusOK, `{"markdown":"# Status report: T1\n","generatedAt":"2026-09-17T10:00:00Z"}`,
			func(t *testing.T, c *client.Client) {
				r, err := c.Report(ctx, "bellek", "task", "t1")
				require.NoError(t, err)
				require.Equal(t, "# Status report: T1\n", r.Markdown)
			},
			http.MethodGet, "/v1/nodes/t1/report?kind=task&projectKey=bellek", "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, got := recording(t, tt.status, tt.answer)
			tt.call(t, c)
			require.Equal(t, tt.method, got.method)
			require.Equal(t, tt.uri, got.uri)
			require.Equal(t, "Bearer "+testKey, got.auth, "every call carries the key")
			if tt.body == "" {
				require.Empty(t, got.body)
				require.Empty(t, got.contentType)
				return
			}
			require.JSONEq(t, tt.body, got.body)
			require.Equal(t, "application/json", got.contentType)
		})
	}
}

func TestCallsReportTheErrorTheServerAnswers(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	tests := []struct {
		name    string
		status  int
		code    string
		message string
		call    func(c *client.Client) error
	}{
		{"a refused key", http.StatusUnauthorized, api.CodeUnauthorized, "missing or invalid key", func(c *client.Client) error {
			_, err := c.Doctor(ctx)
			return err
		}},
		{"a session start without an agent", http.StatusBadRequest, api.CodeValidation, "invalid input: agentKind is required", func(c *client.Client) error {
			_, err := c.SessionStart(ctx, api.SessionStartInput{Cwd: "/x"})
			return err
		}},
		{"an unknown project", http.StatusNotFound, api.CodeNotFound, "project not found", func(c *client.Client) error {
			_, err := c.Export(ctx, "missing", nil)
			return err
		}},
		{"an empty prompt", http.StatusBadRequest, api.CodeValidation, "invalid input: message is required", func(c *client.Client) error {
			_, err := c.Prompt(ctx, id, " ", nil)
			return err
		}},
		{"no paths", http.StatusBadRequest, api.CodeValidation, "invalid input: paths are required", func(c *client.Client) error {
			_, err := c.Artifacts(ctx, id, nil)
			return err
		}},
		{"a file after the session ended", http.StatusConflict, api.CodeSessionEnded, "session has ended", func(c *client.Client) error {
			_, err := c.Artifacts(ctx, id, []string{"internal/auth.go"})
			return err
		}},
		{"progress after the session ended", http.StatusConflict, api.CodeSessionEnded, "session has ended", func(c *client.Client) error {
			return c.Progress(ctx, id, api.ProgressInput{Message: "late"})
		}},
		{"a session ended twice", http.StatusConflict, api.CodeSessionEnded, "session has ended", func(c *client.Client) error {
			return c.SessionEnd(ctx, id, "again")
		}},
		{"a location for an unknown session", http.StatusNotFound, api.CodeNotFound, "session not found", func(c *client.Client) error {
			return c.SessionLocation(ctx, id, api.SessionLocationInput{Cwd: "/repo"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := answering(t, tt.status, fmt.Sprintf(`{"error":{"code":%q,"message":%q}}`, tt.code, tt.message))
			err := tt.call(c)
			var e *client.Error
			require.ErrorAs(t, err, &e)
			require.Equal(t, &client.Error{Status: tt.status, Code: tt.code, Message: tt.message}, e)
			require.True(t, client.IsCode(err, tt.code), err)
		})
	}
	require.False(t, client.IsCode(nil, api.CodeNotFound))
}

func TestClientSessionEndReasonSendsTheReasonInsteadOfASummary(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	c, got := recording(t, http.StatusNoContent, "")

	require.NoError(t, c.SessionEndReason(ctx, id, "session ended by claude code: clear"))
	require.Equal(t, "/v1/sessions/"+id.String()+"/end", got.uri)
	require.JSONEq(t, `{"reason":"session ended by claude code: clear"}`, got.body, "the server builds the handoff from the work, not from this text")

	require.NoError(t, c.SessionEnd(ctx, id, "bye"))
	require.Equal(t, "/v1/sessions/"+id.String()+"/end", got.uri)
	require.JSONEq(t, `{"summary":"bye"}`, got.body)

	ended := answering(t, http.StatusConflict, `{"error":{"code":"sessionEnded","message":"session has ended"}}`)
	err := ended.SessionEndReason(ctx, id, "again")
	require.True(t, client.IsCode(err, api.CodeSessionEnded), err)
}

func TestClientNoteSendsTheBaseVersion(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	c, got := recording(t, http.StatusCreated, `{"kind":"note","key":"rule","version":1}`)

	_, err := c.Note(ctx, api.NoteInput{SessionID: id, Key: "rule", Title: "Rule", Body: "v1"})
	require.NoError(t, err)
	require.NotContains(t, got.body, "baseVersion", "a write without a base version is not checked")
	_, err = c.Note(ctx, api.NoteInput{SessionID: id, Key: "rule", Title: "Rule", Body: "v2", BaseVersion: 1})
	require.NoError(t, err)
	require.JSONEq(t, `{"sessionId":"`+id.String()+`","key":"rule","title":"Rule","body":"v2","baseVersion":1}`, got.body)

	stale := answering(t, http.StatusConflict, `{"error":{"code":"conflict","message":"note rule is at version 2, not 1"}}`)
	_, err = stale.Note(ctx, api.NoteInput{SessionID: id, Key: "rule", Title: "Rule", Body: "stale", BaseVersion: 1})
	require.True(t, client.IsCode(err, api.CodeConflict), err)
	var e *client.Error
	require.ErrorAs(t, err, &e)
	require.Equal(t, http.StatusConflict, e.Status)
	require.Contains(t, e.Message, "version 2, not 1")
}

func TestExportFollowsTheCursorPastOnePage(t *testing.T) {
	ctx := context.Background()
	after := time.Date(2026, 9, 17, 10, 0, 0, 123456789, time.UTC)
	lastID := uuid.New()
	var queries []url.Values
	c := serving(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		snap := api.Snapshot{Project: &api.Node{Kind: api.KindProject, Key: "paged"}}
		size := 5
		if r.URL.Query().Get("afterId") == "" {
			size = 200
			snap.Next = &api.ExportCursor{After: after, AfterID: lastID}
		}
		for i := range size {
			snap.Nodes = append(snap.Nodes, &api.Node{ID: uuid.New(), Kind: api.KindTask, Key: fmt.Sprintf("t%03d", i)})
		}
		_ = json.NewEncoder(w).Encode(snap)
	})

	first, err := c.Export(ctx, "paged", nil)
	require.NoError(t, err)
	require.Len(t, first.Nodes, 200)
	require.NotNil(t, first.Next)
	second, err := c.Export(ctx, "paged", first.Next)
	require.NoError(t, err)
	require.Len(t, second.Nodes, 5)
	require.Nil(t, second.Next)

	require.Empty(t, queries[0], "the first page names no cursor")
	sent, err := time.Parse(time.RFC3339Nano, queries[1].Get("after"))
	require.NoError(t, err)
	require.True(t, sent.Equal(after), "the cursor survives the round trip through the query string")
	require.Equal(t, lastID.String(), queries[1].Get("afterId"))
}

func TestClientDoctorCarriesTheRetentionCounts(t *testing.T) {
	c, got := recording(t, http.StatusOK, `{"nodes":3,"edges":2,"events":9,"orphanEdges":1,"forwardRefs":4,"expiredLeases":5,"staleSessions":6,"maxProjectEvents":600000,"maxEventsProject":"bellek","retainableEvents":42}`)
	rep, err := c.Doctor(context.Background())
	require.NoError(t, err)
	require.Equal(t, "/v1/doctor", got.uri)
	require.Equal(t, api.DoctorReport{Nodes: 3, Edges: 2, Events: 9, OrphanEdges: 1, ForwardRefs: 4, ExpiredLeases: 5, StaleSessions: 6,
		MaxProjectEvents: 600000, MaxEventsProject: "bellek", RetainableEvents: 42}, rep)
}

func TestMeNamesTheKeyItsUserAndItsWorkspace(t *testing.T) {
	c, got := recording(t, http.StatusOK, `{
		"key": {"id": "01999b1a-0000-7000-8000-000000000004", "prefix": "abcd1234", "name": "test", "createdAt": "2026-09-17T10:00:00Z"},
		"user": {"id": "01999b1a-0000-7000-8000-000000000005", "email": "test@example.com", "name": "test"},
		"workspace": {"id": "01999b1a-0000-7000-8000-000000000006", "key": "org-a", "name": "A", "kind": "organization", "role": "admin"}
	}`)
	me, err := c.Me(context.Background())
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, got.method)
	require.Equal(t, "/v1/me", got.uri)
	require.Equal(t, "test", me.Key.Name)
	require.Len(t, me.Key.Prefix, 8)
	require.Equal(t, "test@example.com", me.User.Email)
	require.Equal(t, "org-a", me.Workspace.Key)
	require.Equal(t, api.RoleAdmin, me.Workspace.Role)
	require.True(t, me.Key.ExpiresAt.IsZero(), "a key without an expiry")
}
