package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bellek-si/bellek/api"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestHookStateWritersReportADirectoryTheyCannotCreate(t *testing.T) {
	tests := []struct {
		name  string
		block func(s *hookState) string
		write func(s *hookState) error
	}{
		{"session mapping", func(s *hookState) string { return s.sessions() }, func(s *hookState) error {
			return s.Save("c", uuid.New())
		}},
		{"session name", func(s *hookState) string { return s.sessions() }, func(s *hookState) error {
			return s.SaveName("c", "calm-heron")
		}},
		{"memory marker", func(s *hookState) string { return s.memory() }, func(s *hookState) error {
			return s.SaveMarker("/repo", map[string]markerEntry{"a": {Description: "A"}})
		}},
		{"recall marker", func(s *hookState) string { return s.recall() }, func(s *hookState) error {
			return s.SaveRecalled("c", recallMarker{Files: []string{"a.go"}})
		}},
		{"spool", func(s *hookState) string { return s.spool() }, func(s *hookState) error {
			return s.Spool(spooled{Op: "progress", Message: "did it"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newHookState(t.TempDir())
			require.NoError(t, os.WriteFile(tt.block(s), []byte("not a directory"), 0o600))
			require.Error(t, tt.write(s), "a state directory that cannot be made is reported, not swallowed")
		})
	}
}

func TestSpoolRefusesACaptureItCannotSerialise(t *testing.T) {
	s := newHookState(t.TempDir())
	note := api.NoteInput{Key: "open-channel", Props: map[string]any{"handle": make(chan int)}}

	require.Error(t, s.Spool(spooled{Op: "note", SessionID: uuid.New(), Note: &note}))
	entries, err := os.ReadDir(s.spool())
	require.NoError(t, err)
	require.Empty(t, entries, "nothing half written is left for the next drain")
}

func TestDrainReportsASpoolItCannotRead(t *testing.T) {
	s := newHookState(t.TempDir())
	require.NoError(t, os.WriteFile(s.spool(), []byte("not a directory"), 0o600))

	sent, err := s.Drain(func(spooled) error { return errors.New("nothing should be sent") })
	require.Error(t, err, "a missing spool is not a failure, a broken one is")
	require.Zero(t, sent)
}

func TestDrainSendsNoMoreThanTheLimit(t *testing.T) {
	s := newHookState(t.TempDir())
	for i := 0; i < spoolDrainLimit+2; i++ {
		require.NoError(t, s.Spool(spooled{Op: "progress", Message: fmt.Sprint(i)}))
	}

	var seen []string
	sent, err := s.Drain(func(item spooled) error { seen = append(seen, item.Message); return nil })
	require.NoError(t, err)
	require.Equal(t, spoolDrainLimit, sent)
	require.Equal(t, "0", seen[0], "the oldest go first")
	entries, err := os.ReadDir(s.spool())
	require.NoError(t, err)
	require.Len(t, entries, 2, "the rest wait for the next hook")
}

func TestDrainLeavesEntriesItCannotClaim(t *testing.T) {
	clock := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		setup func(t *testing.T, s *hookState)
	}{
		{
			name: "another hook holds the claim name",
			setup: func(t *testing.T, s *hookState) {
				require.NoError(t, os.WriteFile(filepath.Join(s.spool(), "a.json"), []byte(`{"op":"progress"}`), 0o600))
				require.NoError(t, os.Mkdir(filepath.Join(s.spool(), "a.json.sending"), 0o700))
			},
		},
		{
			name: "a stale claim cannot be taken back",
			setup: func(t *testing.T, s *hookState) {
				require.NoError(t, os.Mkdir(filepath.Join(s.spool(), "b.json"), 0o700))
				claim := filepath.Join(s.spool(), "b.json.sending")
				require.NoError(t, os.WriteFile(claim, []byte(`{"op":"progress"}`), 0o600))
				stale := clock.Add(-2 * spoolStaleSending)
				require.NoError(t, os.Chtimes(claim, stale, stale))
			},
		},
		{
			name: "a directory the hook did not put there",
			setup: func(t *testing.T, s *hookState) {
				require.NoError(t, os.Mkdir(filepath.Join(s.spool(), "c.json"), 0o700))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newHookState(t.TempDir())
			s.now = func() time.Time { return clock }
			require.NoError(t, os.MkdirAll(s.spool(), 0o700))
			tt.setup(t, s)
			before := names(t, s.spool())

			sent, err := s.Drain(func(spooled) error { return errors.New("nothing should be sent") })
			require.NoError(t, err)
			require.Zero(t, sent)
			require.Equal(t, before, names(t, s.spool()), "the drain leaves what it cannot claim exactly as it found it")
		})
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
