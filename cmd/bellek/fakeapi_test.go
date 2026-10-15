package main

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/client"
	"github.com/google/uuid"
)

var errUnreachable = &url.Error{Op: "Post", URL: "http://bellek.test/v1", Err: errors.New("connection refused")}

var errRefusedKey = &client.Error{Status: http.StatusUnauthorized, Code: api.CodeUnauthorized, Message: "missing or invalid key"}

func errInvalid(message string) error {
	return &client.Error{Status: http.StatusBadRequest, Code: api.CodeValidation, Message: message}
}

type fakeAPI struct {
	sessions   []*fakeSession
	noteLog    []api.NoteInput
	sent       []string
	promptAcks [][]uuid.UUID
	calls      []string

	down      bool
	failNotes int
	errs      map[string]error
	refused   map[string]error

	briefing api.Briefing
	prompt   api.PromptResult
	links    map[string][]api.Ref
	editors  map[string][]api.SessionRef
}

type fakeSession struct {
	ID        uuid.UUID
	Key       string
	Start     api.SessionStartInput
	Locations []api.SessionLocationInput
	Prompts   []string
	Artifacts [][]string
	Progress  []api.ProgressInput
	Notes     []api.NoteInput
	Briefings []int
	EndReason string

	ended, gone bool
	focus       *api.Ref
	focusTurns  int
	unread      []api.Signal
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		errs: map[string]error{}, refused: map[string]error{},
		briefing: api.Briefing{Markdown: "## Frontier\n- nothing open\n"},
		links:    map[string][]api.Ref{}, editors: map[string][]api.SessionRef{},
	}
}

func (a *fakeAPI) byID(id uuid.UUID) *fakeSession {
	for _, s := range a.sessions {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (a *fakeAPI) end(id uuid.UUID) { a.byID(id).ended = true }

func (a *fakeAPI) forget(id uuid.UUID) { a.byID(id).gone = true }

func (a *fakeAPI) called(method string) int {
	n := 0
	for _, c := range a.calls {
		if c == method {
			n++
		}
	}
	return n
}

func (a *fakeAPI) notesIn(scope string, kind api.Kind, key string) []api.NoteInput {
	var out []api.NoteInput
	for _, n := range a.noteLog {
		if n.ProjectKey == scope && api.Kind(n.Kind) == kind && n.Key == key {
			out = append(out, n)
		}
	}
	return out
}

func (a *fakeAPI) fail(method string) error {
	a.calls = append(a.calls, method)
	if a.down {
		return errUnreachable
	}
	return a.errs[method]
}

func (a *fakeAPI) live(id uuid.UUID) (*fakeSession, error) {
	s := a.byID(id)
	switch {
	case s == nil || s.gone:
		return nil, &client.Error{Status: http.StatusNotFound, Code: api.CodeNotFound, Message: "session not found"}
	case s.ended:
		return nil, &client.Error{Status: http.StatusConflict, Code: api.CodeSessionEnded, Message: "session has ended"}
	}
	return s, nil
}

func (a *fakeAPI) SessionStart(_ context.Context, in api.SessionStartInput) (*api.SessionStartResult, error) {
	if err := a.fail("SessionStart"); err != nil {
		return nil, err
	}
	id := uuid.New()
	s := &fakeSession{ID: id, Key: "session-" + id.String()[24:], Start: in}
	a.sessions = append(a.sessions, s)
	return &api.SessionStartResult{Session: api.Session{ID: id, Key: s.Key, Status: "active"}, Briefing: a.briefing}, nil
}

func (a *fakeAPI) SessionEndReason(_ context.Context, id uuid.UUID, reason string) error {
	if err := a.fail("SessionEndReason"); err != nil {
		return err
	}
	s, err := a.live(id)
	if err != nil {
		return err
	}
	s.ended, s.EndReason = true, reason
	return nil
}

func (a *fakeAPI) SessionLocation(_ context.Context, id uuid.UUID, in api.SessionLocationInput) error {
	if err := a.fail("SessionLocation"); err != nil {
		return err
	}
	s, err := a.live(id)
	if err != nil {
		return err
	}
	s.Locations = append(s.Locations, in)
	return nil
}

func (a *fakeAPI) Note(_ context.Context, in api.NoteInput) (*api.Node, error) {
	if err := a.fail("Note"); err != nil {
		return nil, err
	}
	if a.failNotes > 0 {
		a.failNotes--
		return nil, errUnreachable
	}
	a.sent = append(a.sent, "note:"+in.Key)
	s, err := a.live(in.SessionID)
	if err != nil {
		return nil, err
	}
	if err := a.refused[in.Key]; err != nil {
		return nil, err
	}
	s.Notes = append(s.Notes, in)
	a.noteLog = append(a.noteLog, in)
	return &api.Node{ID: uuid.New(), Kind: cmp.Or(in.Kind, api.KindNote), Key: in.Key, Title: in.Title}, nil
}

func (a *fakeAPI) Prompt(_ context.Context, id uuid.UUID, message string, ack []uuid.UUID) (*api.PromptResult, error) {
	if err := a.fail("Prompt"); err != nil {
		return nil, err
	}
	a.sent = append(a.sent, "prompt:"+message)
	a.promptAcks = append(a.promptAcks, ack)
	s, err := a.live(id)
	if err != nil {
		return nil, err
	}
	s.Prompts = append(s.Prompts, message)
	s.unread = slices.DeleteFunc(s.unread, func(sig api.Signal) bool { return slices.Contains(ack, sig.ID) })
	res := a.prompt
	res.Turn = len(s.Prompts)
	res.Signals = slices.Clone(s.unread)
	if s.focus != nil {
		s.focusTurns++
		res.Focus, res.FocusTurn = s.focus, s.focusTurns
	}
	return &res, nil
}

func (a *fakeAPI) Artifacts(_ context.Context, id uuid.UUID, paths []string) ([]api.Artifact, error) {
	if err := a.fail("Artifacts"); err != nil {
		return nil, err
	}
	a.sent = append(a.sent, "artifacts:"+strings.Join(paths, ","))
	s, err := a.live(id)
	if err != nil {
		return nil, err
	}
	s.Artifacts = append(s.Artifacts, paths)
	items := make([]api.Artifact, 0, len(paths))
	for _, p := range paths {
		items = append(items, api.Artifact{Key: p, Title: p, Links: a.links[p], Sessions: a.editors[p]})
	}
	return items, nil
}

func (a *fakeAPI) Progress(_ context.Context, id uuid.UUID, in api.ProgressInput) error {
	if err := a.fail("Progress"); err != nil {
		return err
	}
	a.sent = append(a.sent, "progress:"+in.Message)
	s, err := a.live(id)
	if err != nil {
		return err
	}
	s.Progress = append(s.Progress, in)
	return nil
}

func (a *fakeAPI) Briefing(_ context.Context, id uuid.UUID, maxTokens int) (*api.Briefing, error) {
	if err := a.fail("Briefing"); err != nil {
		return nil, err
	}
	s, err := a.live(id)
	if err != nil {
		return nil, err
	}
	s.Briefings = append(s.Briefings, maxTokens)
	b := a.briefing
	return &b, nil
}
