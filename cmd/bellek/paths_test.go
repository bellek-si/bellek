package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bellek-si/bellek/internal/config"
	"github.com/stretchr/testify/require"
)

func TestTouchedPaths(t *testing.T) {
	cwd := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(cwd, "internal"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "internal", "auth.go"), []byte("package auth"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, ".env"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(cwd, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "docs", "api.md"), []byte("# api"), 0o644))
	ignore := []string{".env*", "*.pem", "docs/*"}
	noRoots := func() []string { return nil }
	tests := []struct {
		name  string
		tool  string
		input string
		want  []string
	}{
		{"edit inside cwd", "Edit", `{"file_path":"` + cwd + `/internal/auth.go","old_string":"a","new_string":"b"}`, []string{"internal/auth.go"}},
		{"multi edit", "MultiEdit", `{"file_path":"` + cwd + `/a.go","edits":[{"file_path":"` + cwd + `/b.go"}]}`, []string{"a.go", "b.go"}},
		{"notebook", "NotebookEdit", `{"notebook_path":"` + cwd + `/n.ipynb"}`, []string{"n.ipynb"}},
		{"relative path", "Write", `{"file_path":"internal/new.go"}`, []string{"internal/new.go"}},
		{"ignored env file", "Write", `{"file_path":"` + cwd + `/.env"}`, nil},
		{"ignored pem", "Write", `{"file_path":"` + cwd + `/certs/server.pem"}`, nil},
		{"ignored by a full path glob", "Edit", `{"file_path":"` + cwd + `/docs/api.md"}`, nil},
		{"bash is never a file capture", "Bash", `{"command":"cat internal/auth.go > out.go"}`, nil},
		{"read is never a file capture", "Read", `{"file_path":"` + cwd + `/internal/auth.go"}`, nil},
		{"bad json", "Edit", `not json`, nil},
		{"no paths", "Edit", `{}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, touchedPaths(tt.tool, json.RawMessage(tt.input), cwd, ignore, nil, noRoots))
		})
	}
}

func TestTouchedPathsOutsideCwd(t *testing.T) {
	cwd := t.TempDir()
	worktree := t.TempDir()
	calls := 0
	roots := func() []string { calls++; return []string{cwd, worktree} }

	plan := `{"file_path":"/home/me/.claude/plans/x.md"}`
	require.Nil(t, touchedPaths("Write", json.RawMessage(plan), cwd, nil, nil, roots), "a file outside every worktree is dropped")
	require.Equal(t, 1, calls)

	edits := `{"file_path":"` + worktree + `/cmd/bellek/hook.go","edits":[{"file_path":"` + worktree + `/docs/api.md"},{"file_path":"/etc/hosts"}]}`
	require.Equal(t, []string{"cmd/bellek/hook.go", "docs/api.md"}, touchedPaths("MultiEdit", json.RawMessage(edits), cwd, nil, nil, roots))
	require.Equal(t, 2, calls, "one git call per tool use")

	require.Nil(t, touchedPaths("Write", json.RawMessage(plan), cwd, nil, nil, nil), "no roots means outside cwd is dropped")
	require.Nil(t, touchedPaths("Write", json.RawMessage(`{"file_path":"`+cwd+`"}`), cwd, nil, nil, nil), "cwd itself is not a file")
	nested := filepath.Join(cwd, ".claude", "worktrees", "x")
	inside := func() []string { return []string{cwd, nested} }
	require.Equal(t, []string{"main.go"}, touchedPaths("Write", json.RawMessage(`{"file_path":"`+nested+`/main.go"}`), cwd, nil, nil, inside),
		"a file in a worktree nested under cwd is keyed by that worktree")
}

func TestTouchedPathsFollowsSymlinkedRoots(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))
	roots := func() []string { return []string{real} }
	// git prints the resolved root while Claude Code passes the path the user typed.
	require.Equal(t, []string{"new.go"}, touchedPaths("Write", json.RawMessage(`{"file_path":"`+link+`/new.go"}`), "/elsewhere", nil, nil, roots))
	require.Equal(t, []string{"new.go"}, touchedPaths("Write", json.RawMessage(`{"file_path":"`+real+`/new.go"}`), link, nil, nil, nil))
}

func TestMemoryPaths(t *testing.T) {
	isMemory := func(p string) bool { return filepath.Dir(p) == "/mem" }
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"write", `{"file_path":"/mem/a.md","content":"x"}`, []string{"/mem/a.md"}},
		{"multi edit naming the same file twice", `{"file_path":"/mem/a.md","edits":[{"file_path":"/mem/a.md"},{"file_path":"/mem/b.md"}]}`, []string{"/mem/a.md", "/mem/b.md"}},
		{"ordinary file", `{"file_path":"/repo/main.go"}`, nil},
		{"mixed", `{"file_path":"/repo/main.go","edits":[{"file_path":"/mem/a.md"}]}`, []string{"/mem/a.md"}},
		{"bad json", `not json`, nil},
		{"no paths", `{}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, memoryPaths(json.RawMessage(tt.input), isMemory))
		})
	}
}

func TestTouchedPathsSkipsMemoryFiles(t *testing.T) {
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "fact.md"), []byte("x"), 0o644))
	skip := func(p string) bool { return filepath.Base(p) == "fact.md" }
	input := json.RawMessage(`{"file_path":"` + cwd + `/fact.md"}`)
	require.Nil(t, touchedPaths("Write", input, cwd, nil, skip, nil))
	require.Equal(t, []string{"fact.md"}, touchedPaths("Write", input, cwd, nil, nil, nil))
}

func TestTouchedPathsDedupsAndCaps(t *testing.T) {
	edits := `[`
	for i := 0; i < 40; i++ {
		if i > 0 {
			edits += ","
		}
		edits += `{"file_path":"/r/f` + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `.go"}`
	}
	edits += `]`
	got := touchedPaths("MultiEdit", json.RawMessage(`{"file_path":"/r/faa.go","edits":`+edits+`}`), "/r", nil, nil, nil)
	require.Len(t, got, maxPathsPerCall)
	require.Equal(t, "faa.go", got[0])
}

func TestRelativeToRejectsARelativeEscape(t *testing.T) {
	inside, ok := relativeTo("/repo", "internal/auth.go")
	require.True(t, ok)
	require.Equal(t, "internal/auth.go", inside)

	for _, p := range []string{"../secrets/.env", "..", "../../etc/passwd"} {
		_, ok := relativeTo("/repo", p)
		require.False(t, ok, p)
	}
}

func TestRelativeToNeedsARootForAnAbsolutePath(t *testing.T) {
	_, ok := relativeTo("", "/repo/internal/auth.go")
	require.False(t, ok, "without a root there is nothing to place an absolute path against")

	rel, ok := relativeTo("", "internal/auth.go")
	require.True(t, ok, "a relative path is already relative to the root, whatever it is")
	require.Equal(t, "internal/auth.go", rel)

	require.Empty(t, touchedPaths("Edit", json.RawMessage(`{"file_path":"/repo/internal/auth.go"}`), "", nil, nil, nil),
		"a hook event without a cwd records nothing rather than a path the server cannot place")
}

func TestIgnoredMatchesWhateverTheCase(t *testing.T) {
	globs := []string{".env*", "*.pem", "docs/*"}
	for _, p := range []string{"sub/.env.local", "SUB/.ENV", "Certs/Server.PEM", "DOCS/api.md"} {
		require.True(t, ignored(p, globs), p)
	}
	require.False(t, ignored("main.go", globs))
	require.True(t, ignored("certs/server.pem", []string{"*.PEM"}), "an operator's upper case glob matches a lower case file too")
}

func TestDefaultIgnoreListCoversKeyAndTokenFiles(t *testing.T) {
	globs := strings.Split(config.DefaultCaptureIgnore, ",")
	for _, p := range []string{
		".env", "sub/id_rsa", "id_ed25519.pub", "keys/id_ecdsa", ".netrc", "web/.npmrc", ".pypirc",
		"aws/credentials", "gcloud/application_default_credentials.json", ".git-credentials",
	} {
		require.True(t, ignored(p, globs), p)
	}
	for _, p := range []string{"internal/auth/credentials.go", "main.go", "docs/keys.md"} {
		require.False(t, ignored(p, globs), p)
	}
}
