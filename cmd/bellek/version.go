package main

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout(), Version)
	},
}

func init() {
	Version = buildVersion(Version, debug.ReadBuildInfo)
	rootCmd.AddCommand(versionCmd)
}

// buildVersion keeps the version the Makefile stamps. A go install build has
// none, so it takes the module version Go records in the binary. A build of a
// checkout records its git state, and the version Go derives from that often
// names no release, so that build stays dev.
func buildVersion(stamped string, read func() (*debug.BuildInfo, bool)) string {
	if stamped != "dev" {
		return stamped
	}
	info, ok := read()
	if !ok || !strings.HasPrefix(info.Main.Version, "v") {
		return stamped
	}
	for _, s := range info.Settings {
		if s.Key == "vcs" {
			return stamped
		}
	}
	return info.Main.Version
}
