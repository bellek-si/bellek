package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/client"
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Write a project to markdown files",
	RunE: withClient(func(cmd *cobra.Command, _ *app, c *client.Client) error {
		project, _ := cmd.Flags().GetString("project")
		out, _ := cmd.Flags().GetString("out")
		key, _ := cmd.Flags().GetString("report")
		if key == "" && cmd.Flags().Changed("kind") {
			return errors.New("--kind picks the kind of the --report key, so it needs --report")
		}
		if key != "" {
			kind, _ := cmd.Flags().GetString("kind")
			return writeReport(cmd, c, project, kind, key, out)
		}
		var cursor *api.ExportCursor
		written := 0
		for {
			snap, err := c.Export(cmd.Context(), project, cursor)
			if err != nil {
				return err
			}
			n, err := writeSnapshot(out, snap)
			written += n
			if err != nil {
				return err
			}
			if snap.Next == nil {
				break
			}
			cursor = snap.Next
		}
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %d files to %s\n", written, out)
		return nil
	}),
}

// The file name matches the one the report page downloads.
func writeReport(cmd *cobra.Command, c *client.Client, project, kind, key, out string) error {
	r, err := c.Report(cmd.Context(), project, kind, key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		return err
	}
	path := filepath.Join(out, filepath.Base(key)+"-report.md")
	if err := os.WriteFile(path, []byte(r.Markdown), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
	return nil
}

func init() {
	exportCmd.Flags().String("project", "", "project key")
	exportCmd.Flags().String("out", "", "output directory")
	exportCmd.Flags().String("report", "", "write only the report on this task or session key")
	exportCmd.Flags().String("kind", "task", "kind of the report key: task or session")
	_ = exportCmd.MarkFlagRequired("project")
	_ = exportCmd.MarkFlagRequired("out")
	rootCmd.AddCommand(exportCmd)
}
