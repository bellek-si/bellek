package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/client"
	"github.com/spf13/cobra"
)

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Read markdown files or a plan file into a project",
	RunE: withApp(func(cmd *cobra.Command, a *app) error {
		c := a.client(client.WithTimeout(importTimeout))
		project, _ := cmd.Flags().GetString("project")
		from, _ := cmd.Flags().GetString("from")
		docs, err := readDocs(from)
		if err != nil {
			return err
		}
		batches, err := importBatches(docs)
		if err != nil {
			return err
		}
		var total api.ImportReport
		out := cmd.OutOrStdout()
		for _, batch := range batches {
			rep, err := c.Import(cmd.Context(), project, batch)
			if err != nil {
				printImport(out, total)
				return err
			}
			total.Created += rep.Created
			total.Updated += rep.Updated
			total.Unchanged += rep.Unchanged
			total.Links += rep.Links
			total.Skipped += rep.Skipped
			if rep.Notice != "" {
				total.Notice = rep.Notice
			}
		}
		printImport(out, total)
		if total.Skipped > 0 {
			return fmt.Errorf("%d documents were left out: %s", total.Skipped, total.Notice)
		}
		return nil
	}),
}

func printImport(out io.Writer, r api.ImportReport) {
	fmt.Fprintf(out, "created %d, updated %d, unchanged %d, links %d", r.Created, r.Updated, r.Unchanged, r.Links)
	if r.Skipped > 0 {
		fmt.Fprintf(out, ", left out %d", r.Skipped)
	}
	fmt.Fprintln(out)
}

// A link into a later batch stays a forward link until that batch creates its
// target.
const importBatchBytes = api.MaxRequestBytes * 3 / 4

// importTimeout matches the server's write timeout. One full batch takes over
// the client's default 10 s when every doc is edited.
const importTimeout = time.Minute

func importBatches(docs []api.ImportDoc) ([][]api.ImportDoc, error) {
	var batches [][]api.ImportDoc
	var batch []api.ImportDoc
	size := 0
	for _, d := range docs {
		raw, err := json.Marshal(d)
		if err != nil {
			return nil, err
		}
		if len(batch) > 0 && (len(batch) == api.ImportMaxDocs || size+len(raw) > importBatchBytes) {
			batches = append(batches, batch)
			batch, size = nil, 0
		}
		batch = append(batch, d)
		size += len(raw) + 1
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches, nil
}

func init() {
	importCmd.Flags().String("project", "", "project key")
	importCmd.Flags().String("from", "", "directory or file")
	_ = importCmd.MarkFlagRequired("project")
	_ = importCmd.MarkFlagRequired("from")
	rootCmd.AddCommand(importCmd)
}
