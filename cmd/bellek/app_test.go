package main

import (
	"errors"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestStderrLoggerFallsBackToInfoOnBadLevel(t *testing.T) {
	tests := []struct {
		name  string
		level string
	}{
		{"known level", "debug"},
		{"empty level", ""},
		{"unknown level", "chatty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := stderrLogger(tt.level)
			require.NoError(t, err)
			require.NotNil(t, l)
		})
	}
}

func TestNewAppWithReportsLoggerFailure(t *testing.T) {
	setConfigEnv(t)
	a, err := newAppWith(func(string) (*zap.SugaredLogger, error) { return nil, errors.New("no sink") })
	require.Nil(t, a)
	require.ErrorContains(t, err, "logger:")
	require.ErrorContains(t, err, "no sink")
}

func TestNewAppWithReportsConfigFailure(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("BELLEK_TOKEN", "")
	called := false
	a, err := newAppWith(func(string) (*zap.SugaredLogger, error) {
		called = true
		return nil, nil
	})
	require.Nil(t, a)
	require.ErrorContains(t, err, "config:")
	require.False(t, called)
}

func TestNewAppWithBuildsTheApp(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("LOG_LEVEL", "warn")
	a, err := newAppWith(stderrLogger)
	require.NoError(t, err)
	require.NotNil(t, a.cfg)
	require.NotNil(t, a.log)
	require.Equal(t, "warn", a.cfg.LogLevel)
	a.close()
}

func captureStd(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	fn()
	os.Stdout, os.Stderr = oldOut, oldErr
	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())
	out, err := io.ReadAll(outR)
	require.NoError(t, err)
	errs, err := io.ReadAll(errR)
	require.NoError(t, err)
	return string(out), string(errs)
}

func TestStderrLoggerWritesOnlyToStderrAtItsLevel(t *testing.T) {
	tests := []struct {
		name   string
		level  string
		logged bool
	}{
		{"info logs", "info", true},
		{"debug logs", "debug", true},
		{"warn stays quiet", "warn", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr := captureStd(t, func() {
				l, err := stderrLogger(tt.level)
				require.NoError(t, err)
				l.Infow("mcp call", "tool", "task_next")
				l.Sync()
			})
			require.Empty(t, stdout)
			if tt.logged {
				require.Contains(t, stderr, "mcp call")
				require.Contains(t, stderr, "task_next")
			} else {
				require.Empty(t, stderr)
			}
		})
	}
}

func TestRemoteWarnsWhenTheTokenWouldCrossTheNetworkInClearText(t *testing.T) {
	tests := []struct {
		url  string
		warn bool
	}{
		{closedServer, false},
		{"https://bellek.example.com", false},
		{"http://bellek.example.com", true},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			setConfigEnv(t)
			t.Setenv("BELLEK_URL", tt.url)
			var url string
			_, stderr := captureStd(t, func() {
				a, err := newAppWith(stderrLogger)
				require.NoError(t, err)
				url, _ = a.remote()
				a.close()
			})
			require.Equal(t, tt.url, url)
			if tt.warn {
				require.Contains(t, stderr, "plain http")
			} else {
				require.Empty(t, stderr)
			}
		})
	}
}

func TestEveryWayToTheServerWarnsWhenTheTokenWouldCrossTheNetworkInClearText(t *testing.T) {
	for _, command := range []string{"doctor", "mcp"} {
		t.Run(command, func(t *testing.T) {
			setConfigEnv(t)
			// 0.0.0.0 is not a loopback address, so it counts as another
			// machine, yet a connection to it is refused at once.
			t.Setenv("BELLEK_URL", "http://0.0.0.0:1")
			_, stderr := captureStd(t, func() {
				_, err := runWithStdin(t, "", command)
				require.Error(t, err)
			})
			require.Contains(t, stderr, "plain http", "doctor stands for every command that builds a client, mcp dials on its own")
		})
	}
}
