package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bellek-si/bellek/api"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestWriteSnapshotThenReadDocs(t *testing.T) {
	dir := t.TempDir()
	task := &api.Node{ID: uuid.New(), Kind: api.KindTask, Key: "t1", Title: "T1", Status: "todo", Body: "b", Version: 1, Origin: "import"}
	dep := &api.Node{ID: uuid.New(), Kind: api.KindTask, Key: "t0", Title: "T0", Status: "done", Version: 1, Origin: "import"}
	file := &api.Node{ID: uuid.New(), Kind: api.KindArtifact, Key: "internal/auth/refresh.go", Title: "refresh.go", Version: 1, Origin: "hook"}
	snap := &api.Snapshot{
		Nodes:  []*api.Node{task, dep, file},
		Edges:  map[uuid.UUID][]*api.Edge{task.ID: {{FromID: task.ID, ToID: dep.ID, Type: "dependsOn"}, {FromID: task.ID, ToKey: "ghost", Type: "relatesTo"}}},
		Events: map[uuid.UUID][]*api.Event{task.ID: {{Type: "claimed", Message: "t1"}}},
	}
	n, err := writeSnapshot(dir, snap)
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.FileExists(t, filepath.Join(dir, "tasks", "t1.md"))
	require.FileExists(t, filepath.Join(dir, "artifacts", "internal", "auth", "refresh.go.md"), "a nested artifact key makes its own directories")

	docs, err := readDocs(dir)
	require.NoError(t, err)
	require.Len(t, docs, 2, "import skips artifacts")
	var t1 api.ImportDoc
	for _, d := range docs {
		if d.Key == "t1" {
			t1 = d
		}
	}
	require.Equal(t, "b", t1.Body)
	require.ElementsMatch(t, []api.ImportLink{{Type: "dependsOn", To: "t0", Kind: "task"}, {Type: "relatesTo", To: "ghost"}}, t1.Links)
}

func TestReadDocsParsesPlanFile(t *testing.T) {
	dir := t.TempDir()
	plan := "# Plan\n\n### Task 1: Do it\n\n- [x] **Step 1: a**\n- [ ] **Step 2: b**\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plan.md"), []byte(plan), 0o644))
	docs, err := readDocs(filepath.Join(dir, "plan.md"))
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "do-it", docs[0].Key)
	require.Equal(t, "inProgress", docs[0].Status)
}

func TestReadDocsMixesPlansAndBellekFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "tasks"), 0o755))
	plan := "# Plan\n\n### Task 1: From the plan\n\n- [x] **Step 1: a**\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plan.md"), []byte(plan), 0o644))
	doc := "---\nkind: task\nkey: from-the-file\ntitle: From the file\nstatus: todo\n---\n\nbody\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks", "from-the-file.md"), []byte(doc), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not markdown"), 0o644))

	docs, err := readDocs(dir)
	require.NoError(t, err)
	require.Len(t, docs, 2)
	byKey := map[string]api.ImportDoc{}
	for _, d := range docs {
		byKey[d.Key] = d
	}
	require.Equal(t, "done", byKey["from-the-plan"].Status)
	require.Equal(t, "From the plan", byKey["from-the-plan"].Title)
	require.Equal(t, "todo", byKey["from-the-file"].Status)
	require.Equal(t, "body", byKey["from-the-file"].Body)
}

func TestReadDocsSkipsSessionAndArtifactFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"sessions/s1.md":  "---\nkind: session\nkey: s1\ntitle: S1\n---\n\nbody\n",
		"artifacts/a1.md": "---\nkind: artifact\nkey: a1\ntitle: A1\n---\n\nbody\n",
		"tasks/t1.md":     "---\nkind: task\nkey: t1\ntitle: T1\nstatus: todo\n---\n\nbody\n",
	}
	for name, body := range files {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	docs, err := readDocs(dir)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "t1", docs[0].Key)
}

func TestReadDocsNamesTheFileThatFailsToParse(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "broken.md")
	require.NoError(t, os.WriteFile(bad, []byte("# no front matter and no plan\n"), 0o644))
	_, err := readDocs(dir)
	require.ErrorContains(t, err, "broken.md")
	require.ErrorContains(t, err, "front matter")
}

func TestReadDocsReportsAMissingPath(t *testing.T) {
	_, err := readDocs(filepath.Join(t.TempDir(), "nope"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestWriteSnapshotWithNothingToWrite(t *testing.T) {
	dir := t.TempDir()
	n, err := writeSnapshot(dir, &api.Snapshot{})
	require.NoError(t, err)
	require.Equal(t, 0, n)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "an empty snapshot creates no directories")
}

func TestReadDocsImportsClaudeMemoryFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("- [x](x.md)\n"), 0o644))
	writeMemory(t, dir, "prefers-pnpm", "Use pnpm", "feedback", "2026-09-17T10:00:00.000Z", "Always pnpm.")
	writeMemory(t, dir, "layout", "Where things live", "project", "2026-09-17T10:00:00.000Z", "internal/widgets.")
	docs, err := readDocs(dir)
	require.NoError(t, err)
	require.Len(t, docs, 2)
	byKey := map[string]api.ImportDoc{}
	for _, d := range docs {
		byKey[d.Key] = d
	}
	require.Equal(t, "decision", byKey["prefers-pnpm"].Kind)
	require.Equal(t, "Use pnpm", byKey["prefers-pnpm"].Title)
	require.Equal(t, []string{"memory", "feedback"}, byKey["prefers-pnpm"].Tags)
	require.Equal(t, "claudeMemory", byKey["prefers-pnpm"].Props["source"])
	require.Equal(t, "note", byKey["layout"].Kind)
	require.Equal(t, "internal/widgets.", byKey["layout"].Body)
}

func TestReadDocsImportsOneMemoryFile(t *testing.T) {
	dir := t.TempDir()
	p := writeMemory(t, dir, "prefers-pnpm", "Use pnpm", "feedback", "2026-09-17T10:00:00.000Z", "Always pnpm.")
	docs, err := readDocs(p)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "prefers-pnpm", docs[0].Key)
	require.Equal(t, "decision", docs[0].Kind)
	require.Equal(t, "Always pnpm.", docs[0].Body)
}

func TestReadDocsMixesMemoryAndBellekFiles(t *testing.T) {
	dir := t.TempDir()
	writeMemory(t, dir, "prefers-pnpm", "Use pnpm", "feedback", "2026-09-17T10:00:00.000Z", "Always pnpm.")
	doc := "---\nkind: task\nkey: from-the-file\ntitle: From the file\nstatus: todo\n---\n\nbody\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "from-the-file.md"), []byte(doc), 0o644))
	docs, err := readDocs(dir)
	require.NoError(t, err)
	require.Len(t, docs, 2)
	byKey := map[string]api.ImportDoc{}
	for _, d := range docs {
		byKey[d.Key] = d
	}
	require.Equal(t, "decision", byKey["prefers-pnpm"].Kind)
	require.Equal(t, "task", byKey["from-the-file"].Kind)
	require.Equal(t, "todo", byKey["from-the-file"].Status)
}

func TestReadDocsRefusesAMemoryFileWithABadTimestamp(t *testing.T) {
	dir := t.TempDir()
	writeMemory(t, dir, "prefers-pnpm", "Use pnpm", "feedback", "2026-09-17T10:00:00.000Z", "Always pnpm.")
	writeMemory(t, dir, "layout", "Where things live", "project", "yesterday", "internal/widgets.")

	_, err := readDocs(dir)
	require.ErrorContains(t, err, "layout.md", "a memory whose timestamp is wrong names itself")
	require.ErrorContains(t, err, "modified")
}

func TestWriteSnapshotDropsAnEdgeWithNothingToPointAt(t *testing.T) {
	dir := t.TempDir()
	task := &api.Node{ID: uuid.New(), Kind: api.KindTask, Key: "t1", Title: "T1", Status: "todo", Version: 1, Origin: "import"}
	snap := &api.Snapshot{
		Nodes: []*api.Node{task},
		Edges: map[uuid.UUID][]*api.Edge{task.ID: {
			{FromID: task.ID, ToID: uuid.New(), Type: "dependsOn"},
			{FromID: task.ID, ToKey: "ghost", Type: "relatesTo"},
		}},
	}

	n, err := writeSnapshot(dir, snap)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	docs, err := readDocs(dir)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, []api.ImportLink{{Type: "relatesTo", To: "ghost"}}, docs[0].Links,
		"an edge whose target left the snapshot has no key to write")
}

func TestWriteSnapshotSaysHowManyItWroteBeforeItFailed(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		block func(t *testing.T, dir string)
	}{
		{"a body the codec refuses", "quoting <!-- bellek:timeline --> back", func(*testing.T, string) {}},
		{"a directory that cannot be made", "", func(t *testing.T, dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "notes"), []byte("not a directory"), 0o600))
		}},
		{"a file that cannot be written", "", func(t *testing.T, dir string) {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "notes", "zzz.md"), 0o700))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.block(t, dir)
			snap := &api.Snapshot{Nodes: []*api.Node{
				{ID: uuid.New(), Kind: api.KindTask, Key: "aaa", Title: "A", Version: 1, Origin: "import"},
				{ID: uuid.New(), Kind: api.KindNote, Key: "zzz", Title: "Z", Body: tt.body, Version: 1, Origin: "import"},
			}}

			n, err := writeSnapshot(dir, snap)
			require.Error(t, err)
			require.Equal(t, 1, n, "the count names the files already on disk")
			require.FileExists(t, filepath.Join(dir, "tasks", "aaa.md"))
		})
	}
}

func TestReadDocsReportsAFileItCannotRead(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "good.md"), []byte("---\nkind: task\nkey: t1\ntitle: T1\n---\n\nbody\n"), 0o644))
	dangling := filepath.Join(dir, "zzz-dangling.md")
	require.NoError(t, os.Symlink(filepath.Join(dir, "missing.md"), dangling))

	_, err := readDocs(dir)
	require.Error(t, err, "half an import is worse than none")
	require.ErrorContains(t, err, dangling)
}

func TestWriteSnapshotRefusesAKeyThatLeavesItsKindDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "out")
	for _, key := range []string{"../../escaped", "a/../../../escaped", "../tasks/t1"} {
		evil := &api.Node{ID: uuid.New(), Kind: api.KindNote, Key: key, Title: "x", Version: 1, Origin: "agent"}
		n, err := writeSnapshot(dir, &api.Snapshot{Nodes: []*api.Node{evil}})
		require.ErrorContains(t, err, key)
		require.Zero(t, n)
	}
	require.NoFileExists(t, filepath.Join(root, "escaped.md"))
	require.NoFileExists(t, filepath.Join(dir, "tasks", "t1.md"), "a note cannot overwrite a task file inside the export either")
}

func TestWriteSnapshotKeepsTheExportToItsOwner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	note := &api.Node{ID: uuid.New(), Kind: api.KindNote, Key: "n1", Title: "N1", Version: 1, Origin: "agent"}
	file := &api.Node{ID: uuid.New(), Kind: api.KindArtifact, Key: "internal/auth.go", Title: "auth.go", Version: 1, Origin: "hook"}
	_, err := writeSnapshot(dir, &api.Snapshot{Nodes: []*api.Node{note, file}})
	require.NoError(t, err)
	for _, p := range []string{filepath.Join(dir, "notes", "n1.md"), filepath.Join(dir, "artifacts", "internal", "auth.go.md")} {
		info, err := os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), p)
	}
	for _, p := range []string{dir, filepath.Join(dir, "notes"), filepath.Join(dir, "artifacts", "internal")} {
		info, err := os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o700), info.Mode().Perm(), p)
	}
}

func TestWriteSnapshotNamesALinkToANodeOnAnotherPage(t *testing.T) {
	dir := t.TempDir()
	elsewhere := uuid.New()
	note := &api.Node{ID: uuid.New(), Kind: api.KindNote, Key: "n1", Title: "N1", Version: 1, Origin: "agent"}
	snap := &api.Snapshot{
		Nodes:   []*api.Node{note},
		Edges:   map[uuid.UUID][]*api.Edge{note.ID: {{FromID: note.ID, ToID: elsewhere, Type: "relatesTo"}}},
		Targets: map[uuid.UUID]api.ExportTarget{elsewhere: {Key: "t1", Kind: api.KindTask}},
	}
	_, err := writeSnapshot(dir, snap)
	require.NoError(t, err)
	docs, err := readDocs(dir)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, []api.ImportLink{{Type: "relatesTo", To: "t1", Kind: "task"}}, docs[0].Links)
}

func TestWriteSnapshotRefusesAKindThatLeavesTheExportDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "out")
	evil := &api.Node{ID: uuid.New(), Kind: api.Kind("../agent"), Key: "evil", Title: "x", Version: 1, Origin: "agent"}
	n, err := writeSnapshot(dir, &api.Snapshot{Nodes: []*api.Node{evil}})
	require.NoFileExists(t, filepath.Join(root, "agents", "evil.md"), "the kind directory must stay inside the export directory")
	require.Error(t, err)
	require.Zero(t, n)
}

func TestWriteSnapshotWritesVisibilityOnlyOnNotesAndDecisions(t *testing.T) {
	dir := t.TempDir()
	note := &api.Node{ID: uuid.New(), Kind: api.KindNote, Key: "n1", Title: "N1", Version: 1, Origin: "agent", Visibility: "private"}
	decision := &api.Node{ID: uuid.New(), Kind: api.KindDecision, Key: "d1", Title: "D1", Version: 1, Origin: "agent", Visibility: "shared"}
	task := &api.Node{ID: uuid.New(), Kind: api.KindTask, Key: "t1", Title: "T1", Status: "todo", Version: 1, Origin: "agent", Visibility: "shared"}
	_, err := writeSnapshot(dir, &api.Snapshot{Nodes: []*api.Node{note, decision, task}})
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(dir, "tasks", "t1.md"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "visibility")

	docs, err := readDocs(dir)
	require.NoError(t, err)
	got := map[string]string{}
	for _, d := range docs {
		got[d.Key] = d.Visibility
	}
	require.Equal(t, map[string]string{"n1": "private", "d1": "shared", "t1": ""}, got)
}
