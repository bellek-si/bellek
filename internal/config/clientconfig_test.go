package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const testToken = "bk_xxxxxxxxxxxxxxxx"

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"BELLEK_URL", "BELLEK_TOKEN", "LOG_LEVEL", "BELLEK_CAPTURE", "BELLEK_CAPTURE_IGNORE", "BELLEK_MEMORY_DIRS", "BELLEK_RECALL", "BELLEK_BRIEF_TOKENS"} {
		t.Setenv(k, "")
		require.NoError(t, os.Unsetenv(k))
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("BELLEK_TOKEN", testToken)
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8080", cfg.ServerURL)
	require.Equal(t, testToken, cfg.Token)
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, []string{"prompts", "files", "progress", "memory"}, cfg.Capture)
	require.Empty(t, cfg.MemoryDirs)
	require.Equal(t, []string{".env*", "*.pem", "*.key", "*lock*", "id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*", ".netrc", ".npmrc", ".pypirc", "credentials", "*credentials.json", ".git-credentials"}, cfg.CaptureIgnore)
	require.True(t, cfg.Captures("files"))
	require.False(t, cfg.Captures("thoughts"), "an unknown stream is never captured")
	require.Equal(t, []string{"briefing", "prompt", "files"}, cfg.Recall)
	require.Equal(t, 800, cfg.BriefTokens)
	require.True(t, cfg.Recalls("prompt"))
}

func TestLoadRecall(t *testing.T) {
	tests := []struct {
		name   string
		recall string
		set    bool
		want   []string
	}{
		{"default", "", false, []string{"briefing", "prompt", "files"}},
		{"explicit list", " briefing , files ", true, []string{"briefing", "files"}},
		{"empty turns recall off", "", true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("BELLEK_TOKEN", testToken)
			if tt.set {
				t.Setenv("BELLEK_RECALL", tt.recall)
			}
			t.Setenv("BELLEK_BRIEF_TOKENS", "300")
			cfg, err := Load()
			require.NoError(t, err)
			require.Equal(t, tt.want, cfg.Recall)
			require.Equal(t, 300, cfg.BriefTokens)
		})
	}
	for _, bad := range []string{"0", "-1", "many"} {
		clearEnv(t)
		t.Setenv("BELLEK_TOKEN", testToken)
		t.Setenv("BELLEK_BRIEF_TOKENS", bad)
		_, err := Load()
		require.ErrorContains(t, err, "BELLEK_BRIEF_TOKENS", bad)
	}
}

func TestLoadCaptureOff(t *testing.T) {
	clearEnv(t)
	t.Setenv("BELLEK_TOKEN", testToken)
	t.Setenv("BELLEK_CAPTURE", "")
	t.Setenv("BELLEK_CAPTURE_IGNORE", " *.secret , ")
	t.Setenv("BELLEK_MEMORY_DIRS", "/a/memory,/b/memory")
	cfg, err := Load()
	require.NoError(t, err)
	require.Empty(t, cfg.Capture)
	require.False(t, cfg.Captures("prompts"))
	require.Equal(t, []string{"*.secret"}, cfg.CaptureIgnore)
	require.Equal(t, []string{"/a/memory", "/b/memory"}, cfg.MemoryDirs)
}

func TestAnEmptyListVariableClearsItsDefault(t *testing.T) {
	clearEnv(t)
	t.Setenv("BELLEK_TOKEN", testToken)
	t.Setenv("BELLEK_CAPTURE_IGNORE", "")
	cfg, err := Load()
	require.NoError(t, err)
	require.Empty(t, cfg.CaptureIgnore, "an operator who clears the ignore list has cleared it")
	require.Equal(t, []string{"prompts", "files", "progress", "memory"}, cfg.Capture, "the other lists keep their defaults")
}

func TestAnEmptyVariableThatIsNotAListKeepsItsDefault(t *testing.T) {
	clearEnv(t)
	t.Setenv("BELLEK_TOKEN", testToken)
	for _, k := range []string{"BELLEK_URL", "LOG_LEVEL", "BELLEK_BRIEF_TOKENS"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, DefaultServerURL, cfg.ServerURL)
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, DefaultBriefTokens, cfg.BriefTokens)
}

func TestLoadTrimsTheServerURL(t *testing.T) {
	clearEnv(t)
	t.Setenv("BELLEK_TOKEN", testToken)
	t.Setenv("BELLEK_URL", "https://bellek.example/api///")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "https://bellek.example/api", cfg.ServerURL)
}

func TestLoadReadsTheEnvironmentOnly(t *testing.T) {
	clearEnv(t)
	t.Setenv("BELLEK_DEV", "1")
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, "bellek"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "bellek", ".env"), []byte("BELLEK_URL=http://user.example\nBELLEK_TOKEN=bk_user\n"), 0o600))
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, ".env"), []byte("BELLEK_URL=http://cwd.example\nBELLEK_TOKEN=bk_cwd\n"), 0o600))
	t.Chdir(cwd)

	_, err := Load()
	require.EqualError(t, err, "BELLEK_TOKEN is required", "a key in a file is never used, in dev mode either")

	t.Setenv("BELLEK_TOKEN", testToken)
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8080", cfg.ServerURL, "the URL comes from the environment too")
}

func TestLoadRefusesATokenThatIsNotAKey(t *testing.T) {
	clearEnv(t)
	t.Setenv("BELLEK_TOKEN", "xxxxxxxxxxxxxxxx")
	_, err := Load()
	require.ErrorContains(t, err, "BELLEK_TOKEN must be an API key, which starts with bk_")
}

func TestClearTextIsPlainHTTPToAnotherMachine(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"http://localhost:8080", false},
		{"http://127.0.0.1:8080", false},
		{"http://127.0.0.2:8080", false},
		{"http://[::1]:8080", false},
		{"https://bellek.example.com", false},
		{"http://bellek.example.com", true},
		{"HTTP://bellek.example.com", true},
		{"http://192.168.1.10:8080", true},
		{"http://[2001:db8::1]:8080", true},
		{"http://[bellek", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			require.Equal(t, tt.want, (&Config{ServerURL: tt.url}).ClearText())
		})
	}
}
