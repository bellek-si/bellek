package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaudeProjectName(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"repo path", "/Users/jane/work-space/bellek", "-Users-jane-work-space-bellek"},
		{"worktree path", "/Users/jane/work-space/bellek/.claude/worktrees/x", "-Users-jane-work-space-bellek--claude-worktrees-x"},
		{"trailing slash", "/repo/", "-repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, claudeProjectName(tt.path))
		})
	}
}

func TestMemoryLocatorFindsDirectories(t *testing.T) {
	config := t.TempDir()
	project := filepath.Join(config, "projects", "-repo", "memory")
	require.NoError(t, os.MkdirAll(project, 0o755))
	custom := filepath.Join(t.TempDir(), "custom")
	require.NoError(t, os.MkdirAll(custom, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"autoMemoryDirectory":"`+custom+`"}`), 0o644))
	extra := filepath.Join(t.TempDir(), "extra")
	require.NoError(t, os.MkdirAll(extra, 0o755))

	l := memoryLocator{configDir: config, toplevel: func(string) string { return "/repo" }, extra: []string{extra, "/nowhere", extra}}
	require.Equal(t, []string{project, custom, extra}, l.dirs("/repo/sub"))

	noSettings := memoryLocator{configDir: t.TempDir(), toplevel: func(string) string { return "" }}
	require.Empty(t, noSettings.dirs("/repo"), "a missing project directory is not listed")
	require.Equal(t, []string{custom, extra}, l.dirs(""), "no cwd, only the configured directories")
}

func TestMemoryLocatorIsMemoryFile(t *testing.T) {
	config := t.TempDir()
	custom := filepath.Join(t.TempDir(), "custom")
	extra := filepath.Join(t.TempDir(), "extra")
	l := memoryLocator{configDir: config, extra: []string{extra}}
	require.NoError(t, os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"autoMemoryDirectory":"`+custom+`"}`), 0o644))
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"project memory file", filepath.Join(config, "projects", "-any-name", "memory", "fact.md"), true},
		{"the index", filepath.Join(config, "projects", "-any-name", "memory", "MEMORY.md"), false},
		{"not markdown", filepath.Join(config, "projects", "-any-name", "memory", "fact.txt"), false},
		{"wrong depth", filepath.Join(config, "projects", "memory", "fact.md"), false},
		{"other config dir", "/elsewhere/projects/-x/memory/fact.md", false},
		{"settings directory", filepath.Join(custom, "fact.md"), true},
		{"extra directory", filepath.Join(extra, "fact.md"), true},
		{"relative", "memory/fact.md", false},
		{"repo file", "/repo/docs/memory/notes.md", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, l.isMemoryFile(tt.path))
		})
	}
}

func TestMemoryLocatorSettingsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tests := []struct {
		name     string
		settings string
		want     string
	}{
		{"absolute path", `{"autoMemoryDirectory":"/var/memories"}`, "/var/memories"},
		{"home relative path", `{"autoMemoryDirectory":"~/notes/memory"}`, filepath.Join(home, "notes", "memory")},
		{"no key", `{"other":1}`, ""},
		{"empty value", `{"autoMemoryDirectory":""}`, ""},
		{"malformed json", `{oops`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(config, "settings.json"), []byte(tt.settings), 0o644))
			require.Equal(t, tt.want, memoryLocator{configDir: config}.settingsDir())
		})
	}
	require.Empty(t, memoryLocator{configDir: t.TempDir()}.settingsDir(), "no settings file")
}

func TestMemoryLocatorProjectDir(t *testing.T) {
	config := "/config"
	tests := []struct {
		name     string
		toplevel func(string) string
		cwd      string
		want     string
	}{
		{"repository root", func(string) string { return "/repo" }, "/repo/sub", filepath.Join(config, "projects", "-repo", "memory")},
		{"no repository falls back to cwd", func(string) string { return "" }, "/repo/sub", filepath.Join(config, "projects", "-repo-sub", "memory")},
		{"no toplevel function", nil, "/repo/sub", filepath.Join(config, "projects", "-repo-sub", "memory")},
		{"no cwd", nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, memoryLocator{configDir: config, toplevel: tt.toplevel}.projectDir(tt.cwd))
		})
	}
}

func TestMemoryFilesSkipsTheIndex(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"MEMORY.md", "b.md", "a.md", "notes.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644))
	}
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub.md"), 0o755))
	require.Equal(t, []string{filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md")}, memoryFiles(dir))
	require.Nil(t, memoryFiles(filepath.Join(dir, "missing")))
}

func TestClaudeConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/custom/claude")
	require.Equal(t, "/custom/claude", claudeConfigDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.Equal(t, filepath.Join(home, ".claude"), claudeConfigDir())
}

func TestMemoriseReadsSettingsOnce(t *testing.T) {
	config := t.TempDir()
	path := filepath.Join(config, "settings.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"autoMemoryDirectory":"/facts"}`), 0o600))

	plain := memoryLocator{configDir: config}
	once := plain.memoise()
	require.Equal(t, "/facts", once.settingsDir())

	require.NoError(t, os.Remove(path))
	require.Equal(t, "/facts", once.settingsDir(), "the memoised locator does not read the file again")
	require.Empty(t, plain.settingsDir(), "a locator built without memoise still reads every time")
}
