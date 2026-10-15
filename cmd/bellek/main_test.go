package main

import (
	"bytes"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainReturnsWhenTheCommandSucceeds(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetArgs([]string{"version"})
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetArgs(nil)
	})
	main()
	require.Equal(t, Version+"\n", out.String())
}

func TestMainExitsNonZeroWhenTheCommandFails(t *testing.T) {
	if os.Getenv("BELLEK_TEST_MAIN_CHILD") == "1" {
		os.Args = []string{"bellek", "no-such-command"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainExitsNonZeroWhenTheCommandFails$")
	cmd.Env = append(os.Environ(), "BELLEK_TEST_MAIN_CHILD=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "main returned instead of exiting")
	require.Equal(t, 1, exitErr.ExitCode())
	require.Contains(t, stderr.String(), `unknown command "no-such-command"`)
}
