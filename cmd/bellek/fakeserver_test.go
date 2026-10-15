package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/bellek-si/bellek/api"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const fakeKey = api.KeyPrefix + "fake-test-key"

const exportPage = 200

var taskStatuses = []string{"todo", "inProgress", "blocked", "review", "done", "cancelled"}

type fakeServer struct {
	URL string

	mu       sync.Mutex
	health   api.Health
	me       api.Me
	doctor   api.DoctorReport
	exports  map[string][]*api.Node
	reports  map[string]string
	freeCap  int
	notice   string
	stored   map[string]map[string]api.ImportDoc
	requests []string
}

func serveFake(t *testing.T) *fakeServer {
	t.Helper()
	s := &fakeServer{
		health: api.Health{Status: "ok"},
		me: api.Me{
			Key:       api.Key{ID: uuid.New(), Prefix: "abcd1234", Name: "test"},
			User:      api.User{ID: uuid.New(), Email: "test@example.com", Name: "test"},
			Workspace: api.Workspace{ID: uuid.New(), Key: "org-a", Name: "A", Kind: "organization", Role: api.RoleAdmin},
		},
		exports: map[string][]*api.Node{},
		reports: map[string]string{},
		stored:  map[string]map[string]api.ImportDoc{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/me", s.serveMe)
	mux.HandleFunc("GET /v1/doctor", s.serveDoctor)
	mux.HandleFunc("GET /v1/projects/{key}/export", s.serveExport)
	mux.HandleFunc("POST /v1/projects/{key}/import", s.serveImport)
	mux.HandleFunc("GET /v1/nodes/{key}/report", s.serveReport)
	mux.Handle("/mcp", mcpHandler())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		health := s.health
		s.mu.Unlock()
		if r.URL.Path == "/v1/health" {
			writeJSON(w, http.StatusOK, health)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+fakeKey {
			writeError(w, http.StatusUnauthorized, api.CodeUnauthorized, "missing or invalid key")
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	t.Setenv("BELLEK_URL", srv.URL)
	t.Setenv("BELLEK_TOKEN", fakeKey)
	return s
}

func mcpHandler() http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{Name: "bellek", Version: "test"}, &mcp.ServerOptions{Instructions: "Search before you edit."})
	srv.AddTool(&mcp.Tool{Name: "memory_search", Description: "Search the memory.", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}},
	}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "no hits"}}}, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, api.ErrorBody{Error: api.ErrorDetail{Code: code, Message: message}})
}

func (s *fakeServer) requested(want string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.requests {
		if r == want {
			n++
		}
	}
	return n
}

func (s *fakeServer) serveMe(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.me)
}

func (s *fakeServer) serveDoctor(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.me.Workspace.Role != api.RoleAdmin {
		writeError(w, http.StatusForbidden, api.CodeForbidden, "forbidden")
		return
	}
	writeJSON(w, http.StatusOK, s.doctor)
}

func (s *fakeServer) serveExport(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := r.PathValue("key")
	nodes, ok := s.exports[key]
	if !ok {
		writeError(w, http.StatusNotFound, api.CodeNotFound, "project not found")
		return
	}
	start := 0
	if afterID := r.URL.Query().Get("afterId"); afterID != "" {
		i := slices.IndexFunc(nodes, func(n *api.Node) bool { return n.ID.String() == afterID })
		after, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("after"))
		if i < 0 || err != nil || !after.Equal(nodes[i].UpdatedAt) {
			writeError(w, http.StatusBadRequest, api.CodeBadRequest, "after and afterId name no node of this project")
			return
		}
		start = i + 1
	}
	end := min(start+exportPage, len(nodes))
	snap := api.Snapshot{Project: &api.Node{ID: uuid.New(), Kind: api.KindProject, Key: key}, Nodes: nodes[start:end]}
	if end < len(nodes) {
		last := nodes[end-1]
		snap.Next = &api.ExportCursor{After: last.UpdatedAt, AfterID: last.ID}
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *fakeServer) serveImport(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.MaxRequestBytes))
	if err != nil {
		http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
		return
	}
	var in api.ImportInput
	if err := json.Unmarshal(raw, &in); err != nil {
		writeError(w, http.StatusBadRequest, api.CodeBadRequest, "invalid json body")
		return
	}
	if len(in.Docs) > api.ImportMaxDocs {
		writeError(w, http.StatusBadRequest, api.CodeValidation, fmt.Sprintf("invalid input: an import takes at most %d docs, got %d", api.ImportMaxDocs, len(in.Docs)))
		return
	}
	for _, d := range in.Docs {
		if d.Kind == string(api.KindTask) && d.Status != "" && !slices.Contains(taskStatuses, d.Status) {
			writeError(w, http.StatusBadRequest, api.CodeValidation, fmt.Sprintf("invalid input: status %q is not a task status", d.Status))
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	project := r.PathValue("key")
	if s.stored[project] == nil {
		s.stored[project] = map[string]api.ImportDoc{}
	}
	var rep api.ImportReport
	for _, d := range in.Docs {
		id := d.Kind + "/" + d.Key
		was, ok := s.stored[project][id]
		switch {
		case ok && reflect.DeepEqual(was, d):
			rep.Unchanged++
		case ok:
			rep.Updated++
		case s.freeCap > 0 && s.records() >= s.freeCap:
			rep.Skipped++
			rep.Notice = s.notice
			continue
		default:
			rep.Created++
		}
		s.stored[project][id] = d
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *fakeServer) records() int {
	n := 0
	for _, docs := range s.stored {
		n += len(docs)
	}
	return n
}

func (s *fakeServer) serveReport(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := r.URL.Query()
	md, ok := s.reports[q.Get("projectKey")+"/"+q.Get("kind")+"/"+r.PathValue("key")]
	if !ok {
		writeError(w, http.StatusNotFound, api.CodeNotFound, q.Get("kind")+" "+r.PathValue("key")+" not found")
		return
	}
	writeJSON(w, http.StatusOK, api.Report{Markdown: md, GeneratedAt: time.Now().UTC()})
}

func tasks(n int) []*api.Node {
	at := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	out := make([]*api.Node, 0, n)
	for i := range n {
		stamp := at.Add(time.Duration(i) * time.Second)
		out = append(out, &api.Node{ID: uuid.New(), Kind: api.KindTask, Key: fmt.Sprintf("t%03d", i), Title: "T", Status: "todo",
			Body: "body", Version: 1, Origin: "import", CreatedAt: stamp, UpdatedAt: stamp})
	}
	return out
}
