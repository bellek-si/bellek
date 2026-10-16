package client_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/client"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const testKey = api.KeyPrefix + "test-key"

func serving(t *testing.T, h http.HandlerFunc, opts ...client.Option) *client.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return client.New(srv.URL, testKey, opts...)
}

func answering(t *testing.T, status int, body string) *client.Client {
	t.Helper()
	return serving(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

func TestErrorBodyWithoutJSONBecomesHTTPCode(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"plain text", http.StatusInternalServerError, "boom"},
		{"empty body", http.StatusBadGateway, ""},
		{"json without an error envelope", http.StatusServiceUnavailable, `{"ok":false}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := serving(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			})
			_, err := c.Health(context.Background())
			var e *client.Error
			require.ErrorAs(t, err, &e)
			require.Equal(t, tt.status, e.Status)
			require.Equal(t, "http", e.Code)
			require.Contains(t, e.Message, fmt.Sprint(tt.status))
			require.Contains(t, err.Error(), "http: ")
		})
	}
}

func TestNoContentSkipsDecoding(t *testing.T) {
	c := serving(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	node, err := c.Note(context.Background(), api.NoteInput{SessionID: uuid.New(), Title: "T"})
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, node.ID)
	require.Empty(t, node.Kind)
}

func TestWithTimeoutAborts(t *testing.T) {
	c := serving(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}, client.WithTimeout(30*time.Millisecond))
	start := time.Now()
	_, err := c.Health(context.Background())
	require.Error(t, err)
	require.Less(t, time.Since(start), time.Second, "the client gave up on its own timeout")
	require.False(t, client.IsCode(err, "http"), "a transport failure is not an api error")
}

func TestIsCodeSeesThroughWrapping(t *testing.T) {
	c := answering(t, http.StatusNotFound, `{"error":{"code":"notFound","message":"project not found"}}`)
	_, err := c.Export(context.Background(), "missing", nil)
	require.True(t, client.IsCode(fmt.Errorf("export: %w", err), "notFound"))
	require.False(t, client.IsCode(fmt.Errorf("export: %w", err), "validation"))
	require.False(t, client.IsCode(fmt.Errorf("plain: %s", "notFound"), "notFound"))
}

func TestExportDecodesEdgeAndEventMaps(t *testing.T) {
	base, leaf := uuid.New(), uuid.New()
	c := answering(t, http.StatusOK, fmt.Sprintf(`{
		"project": {"kind": "project", "key": "maps"},
		"nodes": [{"id": %[1]q, "kind": "task", "key": "base"}, {"id": %[2]q, "kind": "task", "key": "leaf"}],
		"edges": {%[2]q: [{"fromId": %[2]q, "toId": %[1]q, "type": "dependsOn"}]},
		"events": {%[1]q: [{"type": "statusChanged", "message": "done"}], %[2]q: []},
		"targets": {}
	}`, base, leaf))

	snap, err := c.Export(context.Background(), "maps", nil)
	require.NoError(t, err)
	byKey := map[string]uuid.UUID{}
	for _, n := range snap.Nodes {
		byKey[n.Key] = n.ID
	}
	require.Equal(t, leaf, byKey["leaf"])
	edges := snap.Edges[byKey["leaf"]]
	require.Len(t, edges, 1, "the map key survives the json round trip as a uuid")
	require.Equal(t, byKey["base"], edges[0].ToID)
	require.Contains(t, snap.Events, byKey["base"], "an events entry is keyed by its node")
}

func TestRequestBuildFailures(t *testing.T) {
	calls := 0
	c := serving(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, "{}")
	})

	_, err := c.Import(context.Background(), "demo", []api.ImportDoc{{Kind: "task", Key: "k", Title: "T", Props: map[string]any{"bad": make(chan int)}}})
	require.ErrorContains(t, err, "json")
	require.Zero(t, calls, "an unmarshallable body never leaves the process")
}

func TestProjectKeyIsEscapedIntoThePath(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{"a slash and a question mark", "a/b?c", "/v1/projects/a%2Fb%3Fc/export"},
		{"a control character", "bad\x7fkey", "/v1/projects/bad%7Fkey/export"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			c := serving(t, func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.EscapedPath()
				_, _ = io.WriteString(w, "{}")
			})
			_, err := c.Export(context.Background(), tt.key, nil)
			require.NoError(t, err)
			require.Equal(t, tt.want, got, "the key stays one path segment and does not retarget the request")
		})
	}
}

func TestTimeoutAndHTTPClientAreOrderIndependent(t *testing.T) {
	own := &http.Client{}
	for _, opts := range [][]client.Option{
		{client.WithTimeout(time.Second), client.WithHTTPClient(own)},
		{client.WithHTTPClient(own), client.WithTimeout(time.Second)},
	} {
		c := serving(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") }, opts...)
		_, err := c.Health(context.Background())
		require.NoError(t, err)
	}
	require.Zero(t, own.Timeout, "the caller's own client is not mutated")
}

func TestTypedCallsPropagateErrors(t *testing.T) {
	ctx := context.Background()
	wrong := answering(t, http.StatusUnauthorized, `{"error":{"code":"unauthorized","message":"missing or invalid key"}}`)

	node, err := wrong.Note(ctx, api.NoteInput{SessionID: uuid.New(), Title: "T"})
	require.Nil(t, node)
	require.True(t, client.IsCode(err, "unauthorized"), err)

	rep, err := wrong.Import(ctx, "demo", []api.ImportDoc{{Kind: "task", Key: "k", Title: "T"}})
	require.Nil(t, rep)
	require.True(t, client.IsCode(err, "unauthorized"), err)

	snap, err := wrong.Export(ctx, "demo", nil)
	require.Nil(t, snap)
	require.True(t, client.IsCode(err, "unauthorized"), err)

	started, err := wrong.SessionStart(ctx, api.SessionStartInput{RepoRemote: "github.com/acme/widgets", AgentKind: "codex"})
	require.Nil(t, started)
	require.True(t, client.IsCode(err, "unauthorized"), err)

	err = wrong.SessionLocation(ctx, uuid.New(), api.SessionLocationInput{RepoRemote: "github.com/acme/widgets"})
	require.True(t, client.IsCode(err, "unauthorized"), err)
}

func TestTheKeyGoesToTheServerHostOnly(t *testing.T) {
	var elsewhere []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere = append(elsewhere, r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	t.Cleanup(other.Close)
	var own []string
	c := serving(t, func(w http.ResponseWriter, r *http.Request) {
		own = append(own, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/v1/health":
			http.Redirect(w, r, "/v1/moved", http.StatusTemporaryRedirect)
		case "/v1/moved":
			http.Redirect(w, r, other.URL+"/v1/health", http.StatusTemporaryRedirect)
		default:
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		}
	})

	h, err := c.Health(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ok", h.Status)
	require.Equal(t, []string{"Bearer " + testKey, "Bearer " + testKey}, own, "a redirect on the server's own host keeps the key")
	require.Equal(t, []string{""}, elsewhere, "a redirect to another host, here the same address on another port, does not carry the key")
}

func TestSessionLocationPostsTheLocation(t *testing.T) {
	var path, body string
	c := serving(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(http.StatusNoContent)
	})
	ctx := context.Background()
	id := uuid.MustParse("01999b1a-0000-7000-8000-000000000001")
	err := c.SessionLocation(ctx, id, api.SessionLocationInput{RepoRemote: "github.com/acme/widgets", Branch: "feat/x"})
	require.NoError(t, err)
	require.Equal(t, "/v1/sessions/"+id.String()+"/location", path)
	require.JSONEq(t, `{"repoRemote":"github.com/acme/widgets","branch":"feat/x"}`, body)

	require.NoError(t, c.SessionLocation(ctx, id, api.SessionLocationInput{RepoRemote: "github.com/acme/widgets"}))
	require.JSONEq(t, `{"repoRemote":"github.com/acme/widgets"}`, body, "an empty branch is left out")
}
