package mcpbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/internal/gitinfo"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
)

type Option func(*options)

type options struct {
	log *zap.SugaredLogger
}

func WithLogger(log *zap.SugaredLogger) Option {
	return func(o *options) { o.log = log }
}

type Locals struct {
	Cwd        string
	RepoRemote string
	RepoRoot   string
	Branch     string
}

func LocalsFrom(cwd string) Locals {
	return localsAt(context.Background(), cwd)
}

func localsAt(ctx context.Context, cwd string) Locals {
	l := Locals{Cwd: cwd, RepoRemote: gitinfo.Remote(ctx, cwd), Branch: gitinfo.Branch(ctx, cwd)}
	if roots := gitinfo.Worktrees(ctx, cwd); len(roots) > 0 {
		l.RepoRoot = roots[0]
	}
	return l
}

const callerGitTimeout = time.Second

func (l Locals) values() map[string]string {
	raw, _ := json.Marshal(api.SessionStartArgs{
		ScopeArgs: api.ScopeArgs{Cwd: l.Cwd, RepoRemote: l.RepoRemote, RepoRoot: l.RepoRoot},
		Branch:    l.Branch,
	})
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	values := map[string]string{}
	for key, field := range fields {
		if s, ok := field.(string); ok && s != "" {
			values[key] = s
		}
	}
	return values
}

type bearerTransport struct {
	token string
	host  string
	next  http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != b.host {
		return b.next.RoundTrip(r)
	}
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(clone)
}

func transport(baseURL, token string) *mcp.StreamableClientTransport {
	var host string
	if u, err := url.Parse(baseURL); err == nil {
		host = u.Host
	}
	return &mcp.StreamableClientTransport{
		Endpoint:             baseURL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerTransport{token: token, host: host, next: http.DefaultTransport}},
		DisableStandaloneSSE: true,
	}
}

func Connect(ctx context.Context, baseURL, token, version string) (*mcp.ClientSession, error) {
	return mcp.NewClient(&mcp.Implementation{Name: "bellek-bridge", Version: version}, nil).Connect(ctx, transport(baseURL, token), nil)
}

func New(ctx context.Context, remote *mcp.ClientSession, locals Locals, version string, opts ...Option) (*mcp.Server, error) {
	o := options{log: zap.NewNop().Sugar()}
	for _, opt := range opts {
		opt(&o)
	}
	list, err := remote.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list remote tools: %w", err)
	}
	var instructions string
	if init := remote.InitializeResult(); init != nil {
		instructions = init.Instructions
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "bellek", Version: version}, &mcp.ServerOptions{Instructions: instructions})
	for _, tool := range list.Tools {
		srv.AddTool(&mcp.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema, Annotations: tool.Annotations},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				args, err := fill(tool.InputSchema, req.Params.Arguments, locals, func(cwd string) Locals {
					ctx, cancel := context.WithTimeout(ctx, callerGitTimeout)
					defer cancel()
					return localsAt(ctx, cwd)
				})
				if err != nil {
					return nil, err
				}
				start := time.Now()
				res, err := remote.CallTool(ctx, &mcp.CallToolParams{Name: tool.Name, Arguments: args})
				durationMs := time.Since(start).Milliseconds()
				if err != nil {
					o.log.Infow("mcp call", "tool", tool.Name, "durationMs", durationMs, "error", err.Error())
					return nil, err
				}
				if res.IsError {
					o.log.Infow("mcp call", "tool", tool.Name, "durationMs", durationMs, "error", firstText(res))
				} else {
					o.log.Infow("mcp call", "tool", tool.Name, "durationMs", durationMs)
				}
				return res, nil
			})
	}
	return srv, nil
}

func firstText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			return t.Text
		}
	}
	return "unknown"
}

func fill(schema any, raw json.RawMessage, locals Locals, locate func(cwd string) Locals) (map[string]any, error) {
	args := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("arguments: %w", err)
		}
	}
	// JSON null leaves the map nil.
	if args == nil {
		args = map[string]any{}
	}
	var caller api.ScopeArgs
	if json.Unmarshal(raw, &caller) == nil && caller.Cwd != "" && caller.Cwd != locals.Cwd {
		locals = locate(caller.Cwd)
	}
	props := schemaProperties(schema)
	for key, value := range locals.values() {
		if _, known := props[key]; !known {
			continue
		}
		if existing, ok := args[key]; ok && existing != nil && existing != "" {
			continue
		}
		args[key] = value
	}
	return args, nil
}

func schemaProperties(schema any) map[string]any {
	m, ok := schema.(map[string]any)
	if !ok {
		raw, err := json.Marshal(schema)
		if err != nil {
			return nil
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil
		}
	}
	props, _ := m["properties"].(map[string]any)
	return props
}
