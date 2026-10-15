package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bellek-si/bellek/api"
	"github.com/google/uuid"
)

const (
	spoolDrainLimit   = 16
	spoolMaxAge       = 7 * 24 * time.Hour
	spoolStaleSending = time.Minute
)

type hookState struct {
	root string
	now  func() time.Time
	seq  uint32
}

func defaultHookState() *hookState {
	if d := os.Getenv("BELLEK_STATE_DIR"); d != "" {
		return newHookState(d)
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return newHookState(filepath.Join(base, "bellek"))
}

func newHookState(root string) *hookState {
	return &hookState{root: root, now: func() time.Time { return time.Now().UTC() }}
}

func (s *hookState) sessions() string { return filepath.Join(s.root, "sessions") }

func (s *hookState) spool() string { return filepath.Join(s.root, "spool") }

func (s *hookState) memory() string { return filepath.Join(s.root, "memory") }

func (s *hookState) recall() string { return filepath.Join(s.root, "recall") }

func (s *hookState) bash() string { return filepath.Join(s.root, "bash") }

func (s *hookState) path(claudeID string) (string, bool) {
	if claudeID == "" || strings.ContainsAny(claudeID, "/\\") || strings.Contains(claudeID, "..") {
		return "", false
	}
	return filepath.Join(s.sessions(), claudeID), true
}

// The name lives beside the mapping so the mapping file keeps its plain id format and a
// session that is live during an upgrade keeps working.
func (s *hookState) namePath(claudeID string) (string, bool) {
	p, ok := s.path(claudeID)
	return p + ".name", ok
}

func (s *hookState) LoadName(claudeID string) string {
	p, ok := s.namePath(claudeID)
	if !ok {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (s *hookState) SaveName(claudeID, name string) error {
	p, ok := s.namePath(claudeID)
	if !ok {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(s.sessions(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(name), 0o600)
}

func (s *hookState) Save(claudeID string, id uuid.UUID) error {
	p, ok := s.path(claudeID)
	if !ok {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(s.sessions(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(id.String()), 0o600)
}

func (s *hookState) Load(claudeID string) (uuid.UUID, bool) {
	p, ok := s.path(claudeID)
	if !ok {
		return uuid.Nil, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(strings.TrimSpace(string(b)))
	return id, err == nil
}

func (s *hookState) Delete(claudeID string) {
	if p, ok := s.path(claudeID); ok {
		_ = os.Remove(p)
		matches, _ := filepath.Glob(filepath.Join(s.bash(), claudeID+"-*.json"))
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
	if p, ok := s.namePath(claudeID); ok {
		_ = os.Remove(p)
	}
	s.DeleteRecall(claudeID)
}

func (s *hookState) recallPath(claudeID string) (string, bool) {
	p, ok := s.path(claudeID)
	if !ok {
		return "", false
	}
	return filepath.Join(s.recall(), filepath.Base(p)+".json"), true
}

func (s *hookState) DeleteRecall(claudeID string) {
	if p, ok := s.recallPath(claudeID); ok {
		_ = os.Remove(p)
	}
}

type recallMarker struct {
	Instructed bool        `json:"instructed"`
	Files      []string    `json:"files"`
	Signals    []uuid.UUID `json:"signals"`
}

func (s *hookState) Recalled(claudeID string) recallMarker {
	var out recallMarker
	p, ok := s.recallPath(claudeID)
	if !ok {
		return out
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func (s *hookState) SaveRecalled(claudeID string, m recallMarker) error {
	p, ok := s.recallPath(claudeID)
	if !ok {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(s.recall(), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(p, raw, 0o600)
}

type spooled struct {
	Op        string         `json:"op"`
	SessionID uuid.UUID      `json:"sessionId"`
	Message   string         `json:"message,omitempty"`
	Paths     []string       `json:"paths,omitempty"`
	Note      *api.NoteInput `json:"note,omitempty"`
	At        time.Time      `json:"at"`
}

type markerEntry struct {
	Modified    time.Time `json:"modified"`
	Description string    `json:"description"`
	Type        string    `json:"type"`
	// Scope is where the name was last sent: empty for the project, else global.
	Scope string `json:"scope,omitempty"`
}

func (s *hookState) markerPath(dir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(dir)))
	return filepath.Join(s.memory(), hex.EncodeToString(sum[:8])+".json")
}

func (s *hookState) LoadMarker(dir string) map[string]markerEntry {
	out := map[string]markerEntry{}
	raw, err := os.ReadFile(s.markerPath(dir))
	if err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func (s *hookState) SaveMarker(dir string, m map[string]markerEntry) error {
	if err := os.MkdirAll(s.memory(), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(s.markerPath(dir), raw, 0o600)
}

func (s *hookState) Spool(item spooled) error {
	if err := os.MkdirAll(s.spool(), 0o700); err != nil {
		return err
	}
	item.At = s.now()
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	// Drain replays in filename order, so the name comes from the same clock
	// the payload does. A counter breaks the tie, because a test clock does
	// not move between two spools in one run.
	s.seq++
	name := fmt.Sprintf("%020d-%06d.json", s.now().UnixNano(), s.seq)
	return os.WriteFile(filepath.Join(s.spool(), name), raw, 0o600)
}

// Spooled requests replay oldest first and stop at the first failure, so order is kept. An entry is
// renamed to .sending while it is in flight, so two hooks draining at once never send
// the same request; one left behind by a killed hook is taken back after spoolStaleSending.
func (s *hookState) Drain(send func(spooled) error) (sent int, err error) {
	entries, err := os.ReadDir(s.spool())
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	names := s.ready(entries)
	cutoff := s.now().Add(-spoolMaxAge)
	tried := 0
	for _, name := range names {
		if tried >= spoolDrainLimit {
			break
		}
		p := filepath.Join(s.spool(), name)
		claimed := p + ".sending"
		if os.Rename(p, claimed) != nil {
			continue
		}
		var item spooled
		raw, err := os.ReadFile(claimed)
		if err != nil || json.Unmarshal(raw, &item) != nil || item.At.Before(cutoff) {
			_ = os.Remove(claimed)
			continue
		}
		tried++
		if err := send(item); err != nil {
			_ = os.Rename(claimed, p)
			return sent, err
		}
		_ = os.Remove(claimed)
		sent++
	}
	return sent, nil
}

func (s *hookState) ready(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		if stale, ok := strings.CutSuffix(name, ".sending"); ok {
			if info, err := e.Info(); err != nil || s.now().Sub(info.ModTime()) < spoolStaleSending {
				continue
			}
			if os.Rename(filepath.Join(s.spool(), name), filepath.Join(s.spool(), stale)) != nil {
				continue
			}
			name = stale
		}
		if strings.HasSuffix(name, ".json") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
