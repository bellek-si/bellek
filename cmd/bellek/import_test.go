package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bellek-si/bellek/api"
	"github.com/stretchr/testify/require"
)

func TestImportBatchesStayUnderTheCountAndTheBodyLimit(t *testing.T) {
	big := strings.Repeat("word ", 8<<10)
	docs := make([]api.ImportDoc, 450)
	for i := range docs {
		docs[i] = api.ImportDoc{Kind: "note", Key: fmt.Sprintf("n%03d", i), Title: "N", Body: "small"}
		if i < 40 {
			docs[i].Body = big
		}
	}
	batches, err := importBatches(docs)
	require.NoError(t, err)
	var back []api.ImportDoc
	for _, b := range batches {
		require.LessOrEqual(t, len(b), api.ImportMaxDocs)
		raw, err := json.Marshal(map[string]any{"docs": b})
		require.NoError(t, err)
		require.Less(t, len(raw), api.MaxRequestBytes, "a batch fits the server's body limit with its envelope")
		back = append(back, b...)
	}
	require.Equal(t, docs, back, "every doc is sent once, in order")

	none, err := importBatches(nil)
	require.NoError(t, err)
	require.Empty(t, none)
}

func writeDocs(t *testing.T, n int, doc func(i int) string) string {
	t.Helper()
	dir := t.TempDir()
	for i := range n {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("d%03d.md", i)), []byte(doc(i)), 0o600))
	}
	return dir
}

func TestImportCommandBatchesPassTheServersBodyLimit(t *testing.T) {
	setConfigEnv(t)
	resetFlags(t, importCmd)
	serveFake(t)
	blob := strings.Repeat("x", 60<<10)
	from := writeDocs(t, 16, func(i int) string {
		return fmt.Sprintf("---\nkind: note\nkey: n%03d\ntitle: N\nprops:\n  blob: %s\n---\n", i, blob)
	})

	out, err := run(t, "import", "--project", "demo", "--from", from)
	require.NoError(t, err, "a full batch, close to 768 KiB, fits under the server's 1 MiB request limit")
	require.Equal(t, "created 16, updated 0, unchanged 0, links 0\n", out)
}

func TestImportCommandStopsAtARefusedBatchAndKeepsTheOnesBefore(t *testing.T) {
	setConfigEnv(t)
	resetFlags(t, importCmd)
	s := serveFake(t)
	from := writeDocs(t, 450, func(i int) string {
		status := "todo"
		if i == 250 {
			status = "finished"
		}
		return fmt.Sprintf("---\nkind: task\nkey: t%03d\ntitle: T\nstatus: %s\n---\n", i, status)
	})

	out, err := run(t, "import", "--project", "demo", "--from", from)
	require.ErrorContains(t, err, `status "finished" is not a task status`)
	require.Equal(t, "created 200, updated 0, unchanged 0, links 0\n", out, "the totals so far are printed before the error")
	require.Equal(t, 2, s.requested("POST /v1/projects/demo/import"), "the batch after the refused one is never sent, as docs/markdown.md says")
	require.Len(t, s.stored["demo"], 200)
	for _, doc := range s.stored["demo"] {
		require.Less(t, doc.Key, "t200", doc.Key)
	}
}

func TestImportCommandSendsNothingWhenADocCannotBeEncoded(t *testing.T) {
	setConfigEnv(t)
	resetFlags(t, importCmd)
	s := serveFake(t)
	from := writeDocs(t, 201, func(i int) string {
		props := ""
		if i == 200 {
			props = "props:\n  ratio: .nan\n"
		}
		return fmt.Sprintf("---\nkind: note\nkey: n%03d\ntitle: N\n%s---\n", i, props)
	})

	_, err := run(t, "import", "--project", "demo", "--from", from)
	require.ErrorContains(t, err, "NaN")
	require.Zero(t, s.requested("POST /v1/projects/demo/import"), "every doc is encoded before the first batch goes out, so a bad one in a later batch stops them all")
}

func TestImportCommandReportsWhatTheFreeCapLeftOut(t *testing.T) {
	setConfigEnv(t)
	resetFlags(t, importCmd)
	s := serveFake(t)
	s.freeCap = 100
	s.notice = "Free holds 100 records, upgrade at https://bellek.test/settings/billing"
	from := writeDocs(t, 103, func(i int) string {
		return fmt.Sprintf("---\nkind: task\nkey: t%03d\ntitle: T\nstatus: todo\n---\n", i)
	})

	out, err := run(t, "import", "--project", "demo", "--from", from)
	require.ErrorContains(t, err, "3 documents were left out: Free holds 100 records, upgrade at https://bellek.test/settings/billing")
	require.Equal(t, "created 100, updated 0, unchanged 0, links 0, left out 3\n", out)
}
