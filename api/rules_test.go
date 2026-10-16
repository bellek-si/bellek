package api_test

import (
	"testing"

	"github.com/bellek-si/bellek/api"
	"github.com/stretchr/testify/require"
)

func TestRepoAddress(t *testing.T) {
	tests := []struct{ remote, want string }{
		{"git@github.com:bellek-si/cloud.git", "github.com/bellek-si/cloud"},
		{"github.com:bellek-si/cloud", "github.com/bellek-si/cloud"},
		{"https://github.com/bellek-si/cloud", "github.com/bellek-si/cloud"},
		{"https://ghp_token@github.com/bellek-si/cloud.git/", "github.com/bellek-si/cloud"},
		{"ssh://git@github.com:22/bellek-si/cloud.git", "github.com/bellek-si/cloud"},
		{"  HTTPS://GitHub.com/Bellek-si/Cloud.git  ", "github.com/bellek-si/cloud"},
		{"https://gitlab.example.com/group/sub/app.git", "gitlab.example.com/group/sub/app"},
		{"https://gitlab.example.com/o/r.git?private_token=glpat-secret", "gitlab.example.com/o/r"},
		{"https://github.com/o/r#token=secret", "github.com/o/r"},
		{"ssh://git@[fe80::1]:22/o/r.git", ""},
		{"git@[fe80::1]:o/r.git", ""},
		{"https://github.com/o/r\n/x", ""},
		{"git@github.com:o/r\x1b[31m", ""},
		{"github.com/bellek-si/cloud", "github.com/bellek-si/cloud"},
		{"", ""},
		{"/srv/git/cloud.git", ""},
		{"../cloud", ""},
		{"file:///srv/git/cloud.git", ""},
		{`C:\repos\cloud`, ""},
		{"cloud", ""},
		{"x:foo.com/o/r", ""},
		{"https://github.com/", ""},
	}
	for _, tt := range tests {
		t.Run(tt.remote, func(t *testing.T) {
			got := api.RepoAddress(tt.remote)
			require.Equal(t, tt.want, got)
			require.Equal(t, got, api.RepoAddress(got), "an address is its own address")
		})
	}
}

func TestRepoAddressDropsWhatACloneAddedToAuthenticate(t *testing.T) {
	tests := []struct{ remote, want string }{
		{"https://x-access-token:secret-for-test@github.com/o/r.git", "github.com/o/r"},
		{"https://oauth2:secret-for-test@gitlab.com/o/r.git", "gitlab.com/o/r"},
		{"https://token-as-user-for-test@dev.azure.com/org/p/_git/r", "dev.azure.com/org/p/_git/r"},
		{"http://x-access-token:secret-for-test@git.internal/o/r.git", "git.internal/o/r"},
		{"ssh://git:secret-for-test@example.com/o/r.git", "example.com/o/r"},
		{"https://deploy:my^secret-for-test@git.example.com/o/r.git", "git.example.com/o/r"},
		{"https://deploy:pässword-for-test@git.example.com/o/r.git", "git.example.com/o/r"},
		{"ssh://git:my|secret-for-test@example.com/o/r.git", "example.com/o/r"},
		{"HTTPS://token-as-user-for-test@github.com/o/r.git", "github.com/o/r"},
		{"https://deploy:p@ss-for-test@git.example.com/o/r.git?x=a@b", "git.example.com/o/r"},
		{"https://token-as-user-for-test@git.example.com", ""},
	}
	for _, tt := range tests {
		got := api.RepoAddress(tt.remote)
		require.Equal(t, tt.want, got, "%q", tt.remote)
		require.NotContains(t, got, "for-test", "%q", tt.remote)
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
