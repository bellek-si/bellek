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

// errUnreachable is what a call returns when nothing answers at the server's address.
var errUnreachable = &url.Error{Op: "Post", URL: "http://bellek.test/v1", Err: errors.New("connection refused")}

var errRefusedKey = &client.Error{Status: http.StatusUnauthorized, Code: api.CodeUnauthorized, Message: "missing or invalid key"}

func errInvalid(message string) error {
	return &client.Error{Status: http.StatusBadRequest, Code: api.CodeValidation, Message: message}
}

// fakeAPI stands in for the server behind hookAPI. It keeps what the hooks
// send and answers what each test sets, refusing an ended or unknown session
// the way the server does.
type fakeAPI struct {
	sessions []*fakeSession
	// noteLog holds every note the server took, oldest first.
	noteLog []api.NoteInput
	// sent lists the captures that reached the server, replays included and
	// refusals too, as op:detail.
	sent []string
	// promptAcks holds the acknowledgements of every prompt that reached the server.
	promptAcks [][]uuid.UUID
	// calls names every method the hooks called, whether it failed or not.
	calls []string

	// down fails every call as if the server were unreachable.
	down bool
	// failNotes fails that many next notes as if the server were unreachable.
	failNotes int
	// errs fails a method, by name, with an error before it reaches the server.
	errs map[string]error
	// refused answers a note with this key with this error.
	refused map[string]error

	briefing api.Briefing
	// prompt is the base of every prompt answer; the turn, the signals and the
	// focus come from the session.
	prompt api.PromptResult
	// links and editors are what an artifacts answer names for a path.
	links   map[string][]api.Ref
	editors map[string][]api.SessionRef
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
	// Briefings holds the token budget of every briefing read.
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

// end ends a session on the server side, as the idle sweep does.
func (a *fakeAPI) end(id uuid.UUID) { a.byID(id).ended = true }

// forget drops a session on the server side, as deleting its project does.
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

// notesIn lists the notes the server took for one scope, kind and key, oldest first.
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
	return &api.Node{ID: uuid.New(), Kind: api.Kind(cmp.Or(in.Kind, string(api.KindNote))), Key: in.Key, Title: in.Title}, nil
}

// Prompt marks the acknowledged signals read and answers the ones still
// unread, so a signal comes back until a prompt acknowledges it.
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
