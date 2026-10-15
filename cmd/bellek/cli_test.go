package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestMask(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{"empty", "", "****"},
		{"four characters", "abcd", "****"},
		{"five characters", "abcde", "ab****de"},
		{"long token", strings.Repeat("x", 16), "xx****xx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, mask(tt.token))
		})
	}
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	})
	err := rootCmd.Execute()
	return out.String(), err
}

func TestVersionCommandPrintsTheVersion(t *testing.T) {
	out, err := run(t, "version")
	require.NoError(t, err)
	require.Equal(t, Version+"\n", out)
}

func setConfigEnv(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("BELLEK_TOKEN", "bk_"+strings.Repeat("x", 16))
	t.Setenv("BELLEK_URL", closedServer)
}

const closedServer = "http://127.0.0.1:1"

func resetFlags(t *testing.T, cmd *cobra.Command) {
	t.Helper()
	t.Cleanup(func() {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
	})
}

func TestConfigCommandFailsOnMissingToken(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("BELLEK_TOKEN", "")

	_, err := run(t, "config")
	require.ErrorContains(t, err, "BELLEK_TOKEN")
}

func TestConfigCommandPrintsTheClientSettings(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("BELLEK_RECALL", "briefing")
	t.Setenv("BELLEK_BRIEF_TOKENS", "300")

	out, err := run(t, "config")
	require.NoError(t, err)
	require.Contains(t, out, "serverUrl="+closedServer+"\n")
	require.Contains(t, out, "token=bk****xx\n")
	require.Contains(t, out, "recall=briefing\n")
	require.Contains(t, out, "briefTokens=300\n")
	require.NotContains(t, out, strings.Repeat("x", 16))
}

func TestConfigCommandPrintsTheLogLevelAndTheLists(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("BELLEK_CAPTURE", "prompts, files")
	t.Setenv("BELLEK_CAPTURE_IGNORE", "")
	t.Setenv("BELLEK_MEMORY_DIRS", "/a/memory,/b/memory")

	out, err := run(t, "config")
	require.NoError(t, err)
	require.Contains(t, out, "logLevel=warn\n")
	require.Contains(t, out, "capture=prompts,files\n")
	require.Contains(t, out, "captureIgnore=\n", "a list the operator cleared prints empty")
	require.Contains(t, out, "memoryDirs=/a/memory,/b/memory\n")
}

func TestRootCommandRejectsUnknownSubcommand(t *testing.T) {
	_, err := run(t, "fly")
	require.Error(t, err)
}

func runWithStdin(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	rootCmd.SetIn(strings.NewReader(stdin))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	return run(t, args...)
}

func TestExportAndImportRequireTheirFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"export without any flag", []string{"export"}, "project"},
		{"export without out", []string{"export", "--project", "p"}, "out"},
		{"import without any flag", []string{"import"}, "project"},
		{"import without from", []string{"import", "--project", "p"}, "from"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := run(t, tt.args...)
			require.ErrorContains(t, err, "required flag")
			require.ErrorContains(t, err, tt.want)
		})
	}
}
