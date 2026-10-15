package markdown

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := Doc{
		Kind: "task", Key: "auth-refresh", Title: "Refresh auth", Status: "inProgress",
		Tags: []string{"auth", "urgent"}, Props: map[string]any{"priority": 2, "owner": "jane"},
		Links:   []Link{{Type: "dependsOn", To: "db-migrate", Kind: "task"}, {Type: "relatesTo", To: "finding-1", Kind: "note"}},
		Version: 3, CreatedAt: time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 16, 11, 0, 0, 0, time.UTC),
		Body: "Rotate tokens.\n\nSee `internal/auth/refresh.go`.\n", Timeline: "- 2026-09-16T10:00:00Z claimed\n",
	}
	b, err := Encode(in)
	require.NoError(t, err)
	require.Contains(t, string(b), "---\nkind: task\n")
	require.Contains(t, string(b), "<!-- bellek:timeline -->")
	out, err := Decode(b)
	require.NoError(t, err)
	require.Equal(t, in.Kind, out.Kind)
	require.Equal(t, in.Key, out.Key)
	require.Equal(t, in.Status, out.Status)
	require.Equal(t, in.Tags, out.Tags)
	require.Equal(t, 2, out.Props["priority"])
	require.Equal(t, in.Links, out.Links)
	require.Equal(t, in.Version, out.Version)
	require.True(t, in.CreatedAt.Equal(out.CreatedAt))
	require.Equal(t, strings.TrimRight(in.Body, "\n"), out.Body)
	require.Equal(t, in.Timeline, out.Timeline)
	again, err := Encode(out)
	require.NoError(t, err)
	require.Equal(t, string(b), string(again))
}

func TestDecodeRejectsMissingFrontMatter(t *testing.T) {
	_, err := Decode([]byte("# just a heading\n"))
	require.Error(t, err)
	_, err = Decode([]byte("---\nkind: task\n"))
	require.Error(t, err)
}

func TestEncodeWithoutTimelineHasNoMarker(t *testing.T) {
	b, err := Encode(Doc{Kind: "note", Key: "n", Title: "N", Body: "hi\n"})
	require.NoError(t, err)
	require.NotContains(t, string(b), "bellek:timeline")
	d, err := Decode(b)
	require.NoError(t, err)
	require.Equal(t, "hi", d.Body)
	require.Empty(t, d.Timeline)
}

func TestDecodeRejectsBadYaml(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"unclosed flow sequence", "---\nkind: [task\n---\n\nbody\n"},
		{"tab indentation", "---\nprops:\n\tpriority: 1\n---\n\nbody\n"},
		{"scalar where a mapping is expected", "---\njust a string\n---\n\nbody\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode([]byte(tt.in))
			require.Error(t, err)
		})
	}
}

func TestDecodeIgnoresUnparsableTimestamps(t *testing.T) {
	d, err := Decode([]byte("---\nkind: task\nkey: t\ntitle: T\ncreatedAt: yesterday\nupdatedAt: \"16/09/2026\"\n---\n\nbody\n"))
	require.NoError(t, err)
	require.True(t, d.CreatedAt.IsZero())
	require.True(t, d.UpdatedAt.IsZero())
	require.Equal(t, "body", d.Body)
}

func TestEncodeOmitsEmptyOptionalKeys(t *testing.T) {
	b, err := Encode(Doc{Kind: "note", Key: "n", Title: "N"})
	require.NoError(t, err)
	out := string(b)
	for _, key := range []string{"props:", "links:", "tags:", "status:", "version:", "createdAt:", "updatedAt:"} {
		require.NotContains(t, out, key)
	}
	d, err := Decode(b)
	require.NoError(t, err)
	require.Nil(t, d.Props)
	require.Nil(t, d.Links)
	require.Nil(t, d.Tags)
}

func TestEncodeRefusesABodyQuotingTheTimelineMarker(t *testing.T) {
	for _, body := range []string{
		"before\n<!-- bellek:timeline -->\nafter",
		"before\n<!-- bellek:timeline -->",
	} {
		_, err := Encode(Doc{Kind: "note", Key: "n", Title: "N", Body: body})
		require.ErrorContains(t, err, "timeline marker", "the body would come back split: %q", body)
		require.ErrorContains(t, err, "note n", "the refusal says which node")
	}

	b, err := Encode(Doc{Kind: "note", Key: "n", Title: "N", Body: "plain", Timeline: "- one\n"})
	require.NoError(t, err)
	back, err := Decode(b)
	require.NoError(t, err)
	require.Equal(t, "plain", back.Body)
	require.Equal(t, "- one\n", back.Timeline)
}

func TestEncodeKeepsSubSecondTimestamps(t *testing.T) {
	at := time.Date(2026, 9, 16, 10, 0, 0, 500_000_000, time.UTC)
	b, err := Encode(Doc{Kind: "note", Key: "n", Title: "N", CreatedAt: at, UpdatedAt: at})
	require.NoError(t, err)

	out, err := Decode(b)
	require.NoError(t, err)
	require.True(t, at.Equal(out.CreatedAt), "createdAt lost its fraction: %s", out.CreatedAt)
	require.True(t, at.Equal(out.UpdatedAt), "updatedAt lost its fraction: %s", out.UpdatedAt)
}

func TestEncodeDecodeKeepsVisibility(t *testing.T) {
	b, err := Encode(Doc{Kind: "note", Key: "n1", Title: "N1", Visibility: "private"})
	require.NoError(t, err)
	require.Contains(t, string(b), "\nvisibility: private\n")
	out, err := Decode(b)
	require.NoError(t, err)
	require.Equal(t, "private", out.Visibility)

	b, err = Encode(Doc{Kind: "note", Key: "n1", Title: "N1"})
	require.NoError(t, err)
	require.NotContains(t, string(b), "visibility")
}
