package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/internal/bearer"
	"github.com/google/uuid"
)

const defaultTimeout = 10 * time.Second

type Client struct {
	base    string
	token   string
	http    *http.Client
	timeout time.Duration
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// New never changes the http.Client given to WithHTTPClient, and the options
// may come in any order.
func New(baseURL, token string, opts ...Option) *Client {
	c := &Client{base: strings.TrimRight(baseURL, "/"), token: token, http: &http.Client{Timeout: defaultTimeout}}
	for _, o := range opts {
		o(c)
	}
	h := *c.http
	if c.timeout > 0 {
		h.Timeout = c.timeout
	}
	next := h.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	h.Transport = bearer.New(c.base, token, next)
	c.http = &h
	return c
}

type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

func (c *Client) Health(ctx context.Context) (*api.Health, error) {
	return call[api.Health](ctx, c, http.MethodGet, "/v1/health", nil)
}

func (c *Client) SessionStart(ctx context.Context, in api.SessionStartInput) (*api.SessionStartResult, error) {
	return call[api.SessionStartResult](ctx, c, http.MethodPost, "/v1/sessions", in)
}

func (c *Client) SessionEnd(ctx context.Context, id uuid.UUID, summary string) error {
	return c.do(ctx, http.MethodPost, "/v1/sessions/"+id.String()+"/end", api.SessionEndInput{Summary: summary}, nil)
}

// SessionEndReason ends a session with why it ended instead of a summary, so the server builds
// each task's handoff from the work recorded on it.
func (c *Client) SessionEndReason(ctx context.Context, id uuid.UUID, reason string) error {
	return c.do(ctx, http.MethodPost, "/v1/sessions/"+id.String()+"/end", api.SessionEndInput{Reason: reason}, nil)
}

func (c *Client) SessionLocation(ctx context.Context, id uuid.UUID, in api.SessionLocationInput) error {
	return c.do(ctx, http.MethodPost, "/v1/sessions/"+id.String()+"/location", in, nil)
}

func (c *Client) Briefing(ctx context.Context, id uuid.UUID, maxTokens int) (*api.Briefing, error) {
	q := "?sessionId=" + id.String()
	if maxTokens > 0 {
		q += "&maxTokens=" + strconv.Itoa(maxTokens)
	}
	return call[api.Briefing](ctx, c, http.MethodGet, "/v1/briefing"+q, nil)
}

func (c *Client) Prompt(ctx context.Context, id uuid.UUID, message string, ack []uuid.UUID) (*api.PromptResult, error) {
	return call[api.PromptResult](ctx, c, http.MethodPost, "/v1/sessions/"+id.String()+"/prompt", api.PromptInput{Message: message, AckSignals: ack})
}

func (c *Client) Artifacts(ctx context.Context, id uuid.UUID, paths []string) ([]api.Artifact, error) {
	out, err := call[api.ArtifactsResult](ctx, c, http.MethodPost, "/v1/sessions/"+id.String()+"/artifacts", api.ArtifactsInput{Paths: paths})
	if err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) Progress(ctx context.Context, id uuid.UUID, in api.ProgressInput) error {
	return c.do(ctx, http.MethodPost, "/v1/sessions/"+id.String()+"/progress", in, nil)
}

func (c *Client) Note(ctx context.Context, in api.NoteInput) (*api.Node, error) {
	return call[api.Node](ctx, c, http.MethodPost, "/v1/notes", in)
}

func (c *Client) Export(ctx context.Context, project string, after *api.ExportCursor) (*api.Snapshot, error) {
	path := "/v1/projects/" + url.PathEscape(project) + "/export"
	if after != nil {
		path += "?" + url.Values{"after": {after.After.Format(time.RFC3339Nano)}, "afterId": {after.AfterID.String()}}.Encode()
	}
	return call[api.Snapshot](ctx, c, http.MethodGet, path, nil)
}

func (c *Client) Import(ctx context.Context, project string, docs []api.ImportDoc) (*api.ImportReport, error) {
	return call[api.ImportReport](ctx, c, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/import", api.ImportInput{Docs: docs})
}

// Report fetches the markdown report on a node; kind is task or session.
func (c *Client) Report(ctx context.Context, project, kind, key string) (*api.Report, error) {
	query := url.Values{"projectKey": {project}, "kind": {kind}}.Encode()
	return call[api.Report](ctx, c, http.MethodGet, "/v1/nodes/"+url.PathEscape(key)+"/report?"+query, nil)
}

func (c *Client) Me(ctx context.Context) (*api.Me, error) {
	return call[api.Me](ctx, c, http.MethodGet, "/v1/me", nil)
}

func (c *Client) Doctor(ctx context.Context) (api.DoctorReport, error) {
	var out api.DoctorReport
	err := c.do(ctx, http.MethodGet, "/v1/doctor", nil, &out)
	return out, err
}

func call[T any](ctx context.Context, c *Client, method, path string, body any) (*T, error) {
	var out T
	if err := c.do(ctx, method, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		var envelope api.ErrorBody
		_ = json.NewDecoder(res.Body).Decode(&envelope)
		if envelope.Error.Code == "" {
			envelope.Error.Code = "http"
			envelope.Error.Message = res.Status
		}
		return &Error{Status: res.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message}
	}
	if out == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}
