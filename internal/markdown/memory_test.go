package markdown

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const sampleMemory = `---
name: drafts-stay-local
description: "Draft notes stay uncommitted and local only"
metadata:
  node_type: memory
  type: feedback
  originSessionId: 11111111-2222-4333-8444-555555555555
  modified: 2026-09-15T17:36:55.535Z
---

The notes live at ` + "`notes/`" + ` and must never be committed.

**Why:** The user said so.
`

func TestDecodeMemory(t *testing.T) {
	m, err := DecodeMemory([]byte(sampleMemory))
	require.NoError(t, err)
	require.Equal(t, "drafts-stay-local", m.Name)
	require.Equal(t, "Draft notes stay uncommitted and local only", m.Description)
	require.Equal(t, "feedback", m.Type)
	require.Equal(t, "11111111-2222-4333-8444-555555555555", m.OriginSessionID)
	require.Equal(t, time.Date(2026, 9, 15, 17, 36, 55, 535000000, time.UTC), m.Modified.UTC())
	require.Equal(t, "The notes live at `notes/` and must never be committed.\n\n**Why:** The user said so.", m.Body)
}

func TestDecodeMemoryRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"missing fence", "name: x\n", "missing front matter"},
		{"unterminated fence", "---\nname: x\n", "unterminated front matter"},
		{"no name", "---\ndescription: d\nmetadata:\n  type: user\n---\nbody\n", "no name"},
		{"no type", "---\nname: x\ndescription: d\n---\nbody\n", "no metadata type"},
		{"bad yaml", "---\nname: [\n---\nbody\n", "yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeMemory([]byte(tt.in))
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestDecodeMemoryTakesAnUnquotedDescriptionAndAnEmptyBody(t *testing.T) {
	m, err := DecodeMemory([]byte("---\nname: x\ndescription: Use pnpm, never npm\nmetadata:\n  type: user\n---\n"))
	require.NoError(t, err)
	require.Equal(t, "Use pnpm, never npm", m.Description)
	require.Empty(t, m.Body)
}

func TestDecodeMemoryTellsNotAMemoryFromABrokenOne(t *testing.T) {
	_, err := DecodeMemory([]byte(sampleMemory))
	require.NoError(t, err)

	b, err := Encode(Doc{Kind: "note", Key: "k", Title: "T", Body: "b"})
	require.NoError(t, err)
	for name, in := range map[string][]byte{
		"a bellek export document":      b,
		"a memory index":                []byte("# MEMORY\n- [x](x.md)\n"),
		"a plan":                        []byte("# Plan\n\n### Task 1: Ship it\n\n- [ ] step one\n"),
		"front matter with no metadata": []byte("---\nname: x\n---\nno metadata\n"),
	} {
		_, err := DecodeMemory(in)
		require.ErrorIs(t, err, ErrNotMemory, name)
	}

	_, err = DecodeMemory([]byte("---\nname: x\nmetadata:\n  type: user\n  modified: yesterday\n---\nbody\n"))
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNotMemory, "it is a memory, its timestamp is wrong")
	require.ErrorContains(t, err, "modified")
}

func TestDecodeMemoryReadsTheScope(t *testing.T) {
	for in, want := range map[string]string{"project": "project", "global": "global", "": "", "team": ""} {
		head := "---\nname: x\nmetadata:\n  type: feedback\n"
		if in != "" {
			head += "  scope: " + in + "\n"
		}
		m, err := DecodeMemory([]byte(head + "---\nbody\n"))
		require.NoError(t, err)
		require.Equal(t, want, m.Scope, in)
	}
}
