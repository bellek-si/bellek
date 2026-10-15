package client_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/client"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestBriefingSendsTheBudgetOnlyWhenItIsSet(t *testing.T) {
	var urls []string
	c := serving(t, func(w http.ResponseWriter, r *http.Request) {
		urls = append(urls, r.URL.String())
		_, _ = w.Write([]byte(`{"markdown":"## Frontier\n- one\n","sections":[],"truncated":true}`))
	})
	id := uuid.MustParse("01999b1a-0000-7000-8000-000000000001")
	ctx := context.Background()

	b, err := c.Briefing(ctx, id, 800)
	require.NoError(t, err)
	require.Contains(t, b.Markdown, "## Frontier")
	require.True(t, b.Truncated)

	for _, budget := range []int{0, -5} {
		_, err = c.Briefing(ctx, id, budget)
		require.NoError(t, err)
	}
	require.Equal(t, []string{
		"/v1/briefing?sessionId=" + id.String() + "&maxTokens=800",
		"/v1/briefing?sessionId=" + id.String(),
		"/v1/briefing?sessionId=" + id.String(),
	}, urls)
}

func TestArtifactsDecodeTheirLinks(t *testing.T) {
	c := serving(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"items":[{"key":"internal/auth.go","title":"auth.go","links":[{"kind":"task","key":"auth-refresh","title":"Refresh","status":"inProgress"}]},{"key":"README.md","links":[]}]}`))
	})
	items, err := c.Artifacts(context.Background(), uuid.New(), []string{"internal/auth.go", "README.md"})
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "internal/auth.go", items[0].Key)
	require.Len(t, items[0].Links, 1)
	require.Equal(t, api.KindTask, items[0].Links[0].Kind)
	require.Equal(t, "auth-refresh", items[0].Links[0].Key)
	require.Equal(t, "inProgress", items[0].Links[0].Status)
	require.Empty(t, items[1].Links)
}

func TestBriefingReportsAServerError(t *testing.T) {
	c := serving(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"notFound","message":"no session"}}`))
	})
	_, err := c.Briefing(context.Background(), uuid.New(), 100)
	require.True(t, client.IsCode(err, "notFound"), err)
}

func TestPromptSendsAcknowledgementsOnlyWhenGivenAndDecodesSignals(t *testing.T) {
	var bodies []string
	c := serving(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"turn":1,"related":[],"signals":[{"id":"01999b1a-0000-7000-8000-000000000002","fromSessionKey":"session-7b3a","fromFocusKey":"auth-refresh","kind":"request","threadId":"t1","message":"401 or 403?"}]}`))
	})
	id := uuid.MustParse("01999b1a-0000-7000-8000-000000000001")
	ack := uuid.MustParse("01999b1a-0000-7000-8000-000000000009")
	res, err := c.Prompt(context.Background(), id, "hi", nil)
	require.NoError(t, err)
	require.Len(t, res.Signals, 1)
	require.Equal(t, "session-7b3a", res.Signals[0].FromSessionKey)
	require.Equal(t, "t1", res.Signals[0].ThreadID)
	_, err = c.Prompt(context.Background(), id, "hi", []uuid.UUID{ack})
	require.NoError(t, err)
	require.JSONEq(t, `{"message":"hi"}`, bodies[0])
	require.JSONEq(t, `{"ackSignals":["`+ack.String()+`"],"message":"hi"}`, bodies[1])
}

func TestArtifactsDecodeTheirSessions(t *testing.T) {
	c := serving(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"items":[{"key":"internal/auth.go","links":[],"sessions":[{"key":"session-7b3a","focusKey":"auth-refresh","agentId":"mac","branch":"main","agoSeconds":240}]}]}`))
	})
	items, err := c.Artifacts(context.Background(), uuid.New(), []string{"internal/auth.go"})
	require.NoError(t, err)
	require.Equal(t, api.SessionRef{Key: "session-7b3a", FocusKey: "auth-refresh", AgentID: "mac", Branch: "main", AgoSeconds: 240}, items[0].Sessions[0])
}

func TestTheBriefingCarriesThePlanNotice(t *testing.T) {
	c := serving(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"session":{"id":"01999b1a-0000-7000-8000-000000000001","key":"session-1"},"briefing":{"markdown":"","truncated":false,"notice":"full"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"markdown":"## Frontier\n","truncated":false,"notice":"full"}`))
	})
	ctx := context.Background()
	b, err := c.Briefing(ctx, uuid.New(), 0)
	require.NoError(t, err)
	require.Equal(t, "full", b.Notice)
	started, err := c.SessionStart(ctx, api.SessionStartInput{})
	require.NoError(t, err)
	require.Equal(t, "full", started.Briefing.Notice, "a new session hears it too")
}
