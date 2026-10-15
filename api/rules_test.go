package api_test

import (
	"testing"

	"github.com/bellek-si/bellek/api"
	"github.com/stretchr/testify/require"
)

func TestRemoteWithoutCredentialsDropsWhatACloneAddedToAuthenticate(t *testing.T) {
	tests := []struct{ remote, want string }{
		{"https://x-access-token:secret-for-test@github.com/o/r.git", "https://github.com/o/r.git"},
		{"https://oauth2:secret-for-test@gitlab.com/o/r.git", "https://gitlab.com/o/r.git"},
		{"https://token-as-user-for-test@dev.azure.com/org/p/_git/r", "https://dev.azure.com/org/p/_git/r"},
		{"http://x-access-token:secret-for-test@git.internal/o/r.git", "http://git.internal/o/r.git"},
		{"ssh://git:secret-for-test@example.com/o/r.git", "ssh://git@example.com/o/r.git"},
		{"https://deploy:my^secret-for-test@git.example.com/o/r.git", "https://git.example.com/o/r.git"},
		{"https://deploy:pässword-for-test@git.example.com/o/r.git", "https://git.example.com/o/r.git"},
		{"ssh://git:my|secret-for-test@example.com/o/r.git", "ssh://git@example.com/o/r.git"},
		{"HTTPS://token-as-user-for-test@github.com/o/r.git", "HTTPS://github.com/o/r.git"},
		{"https://deploy:p@ss-for-test@git.example.com/o/r.git?x=a@b", "https://git.example.com/o/r.git?x=a@b"},
		{"https://token-as-user-for-test@git.example.com", "https://git.example.com"},
		{"https://git.example.com?x=a@b", "https://git.example.com?x=a@b"},
		{"https://git.example.com#a@b", "https://git.example.com#a@b"},
		{"ssh://git@[fe80::1]/o/r.git", "ssh://git@[fe80::1]/o/r.git"},
		{"ssh://git@github.com/o/r.git", "ssh://git@github.com/o/r.git"},
		{"git@github.com:o/r.git", "git@github.com:o/r.git"},
		{"https://github.com/o/r.git", "https://github.com/o/r.git"},
		{"/srv/git/r.git", "/srv/git/r.git"},
		{"", ""},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, api.RemoteWithoutCredentials(tt.remote), "%q", tt.remote)
	}
}

func TestCanonicalUUIDKeepsOnlyTheFormClaudeResumeTakes(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"c0ffee00-1111-2222-3333-444455556666", "c0ffee00-1111-2222-3333-444455556666", true},
		{"C0FFEE00-1111-2222-3333-444455556666", "c0ffee00-1111-2222-3333-444455556666", true},
		{"c0ffee00111122223333444455556666", "", false},
		{"{c0ffee00-1111-2222-3333-444455556666}", "", false},
		{"urn:uuid:c0ffee00-1111-2222-3333-444455556666", "", false},
		{"c", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := api.CanonicalUUID(tt.in)
		require.Equal(t, tt.want, got, "%q", tt.in)
		require.Equal(t, tt.ok, ok, "%q", tt.in)
	}
}

func TestOneLineTurnsEveryLineBreakIntoASpace(t *testing.T) {
	got := api.OneLine(" a\nb\rc\td\u0085e\u2028f\u2029g\x00h\n")
	require.Equal(t, "a b c d e f g h", got, "newline, return, tab, next line, line and paragraph separators and NUL")
}
