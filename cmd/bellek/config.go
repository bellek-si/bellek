package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Print the loaded configuration",
	RunE: withApp(func(cmd *cobra.Command, a *app) error {
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "serverUrl=%s\n", a.cfg.ServerURL)
		fmt.Fprintf(out, "token=%s\n", mask(a.cfg.Token))
		fmt.Fprintf(out, "logLevel=%s\n", a.cfg.LogLevel)
		fmt.Fprintf(out, "capture=%s\n", strings.Join(a.cfg.Capture, ","))
		fmt.Fprintf(out, "captureIgnore=%s\n", strings.Join(a.cfg.CaptureIgnore, ","))
		fmt.Fprintf(out, "memoryDirs=%s\n", strings.Join(a.cfg.MemoryDirs, ","))
		fmt.Fprintf(out, "recall=%s\n", strings.Join(a.cfg.Recall, ","))
		fmt.Fprintf(out, "briefTokens=%d\n", a.cfg.BriefTokens)
		return nil
	}),
}

func mask(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "****" + s[len(s)-2:]
}

func init() {
	rootCmd.AddCommand(configCmd)
}
