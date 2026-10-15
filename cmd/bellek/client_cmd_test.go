package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bellek-si/bellek/api"
	"github.com/stretchr/testify/require"
)

const minClientVersion = "v0.1.0"

func TestConfigCommandPrintsTheServerURL(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("BELLEK_URL", "https://bellek.example/")

	out, err := run(t, "config")
	require.NoError(t, err)
	require.Contains(t, out, "serverUrl=https://bellek.example\n", "the trailing slash is trimmed once, at load")
}

func TestDoctorCommandReportsAnUnreachableServer(t *testing.T) {
	setConfigEnv(t)
	_, err := run(t, "doctor")
	require.Error(t, err)
	require.ErrorContains(t, err, "server unreachable")
	require.ErrorContains(t, err, closedServer, "the message names the server it tried")
}

func TestMCPCommandFailsWhenTheServerIsDown(t *testing.T) {
	setConfigEnv(t)
	_, err := runWithStdin(t, "", "mcp")
	require.Error(t, err, "the bridge has nothing to bridge to")
}

func TestImportCommandFailsBeforeReachingTheServer(t *testing.T) {
	setConfigEnv(t)
	resetFlags(t, importCmd)
	missing := filepath.Join(t.TempDir(), "not-here")

	_, err := run(t, "import", "--project", "p", "--from", missing)
	require.Error(t, err)
	require.ErrorContains(t, err, missing)
	require.NotContains(t, err.Error(), "connection refused", "the files are read before any request goes out")
}

func TestExportCommandReportsAnUnreachableServer(t *testing.T) {
	setConfigEnv(t)
	resetFlags(t, exportCmd)

	_, err := run(t, "export", "--project", "p", "--out", t.TempDir())
	require.Error(t, err)
}

func TestDoctorCommandReportsWhatTheServerCounts(t *testing.T) {
	tests := []struct {
		name    string
		report  api.DoctorReport
		want    []string
		wantErr string
	}{
		{"a clean database", api.DoctorReport{}, []string{"nodes: 0, edges: 0, events: 0\n", "orphan edges: 0\n", "largest project: none\n", "retainable events: 0\n"}, ""},
		{"an edge with no node behind it", api.DoctorReport{Edges: 1, OrphanEdges: 1}, []string{"nodes: 0, edges: 1, events: 0\n", "orphan edges: 1\n"}, "doctor found problems"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setConfigEnv(t)
			s := serveFake(t)
			s.doctor = tt.report

			out, err := run(t, "doctor")
			require.Contains(t, out, "server: ok ("+s.URL+")\n")
			for _, line := range tt.want {
				require.Contains(t, out, line)
			}
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func withVersion(t *testing.T, v string) {
	t.Helper()
	was := Version
	Version = v
	t.Cleanup(func() { Version = was })
}

func TestDoctorCommandComparesTheClientWithTheServer(t *testing.T) {
	tests := []struct {
		name, client string
		old          bool
	}{
		{"a supported client", minClientVersion, false},
		{"a client older than the server serves", "v0.0.9", true},
		{"a build from a checkout", "dev", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setConfigEnv(t)
			withVersion(t, tt.client)
			s := serveFake(t)
			s.health = api.Health{Status: "ok", Version: "v9.9.9", MinClientVersion: minClientVersion}

			out, err := run(t, "doctor")
			require.Contains(t, out, "versions: client "+tt.client+", server v9.9.9\n")
			require.Contains(t, out, "nodes: 0, edges: 0, events: 0\n", "an old client still reads the counts")
			if tt.old {
				require.Contains(t, out, "this client is older than "+minClientVersion)
				require.ErrorContains(t, err, "doctor found problems")
			} else {
				require.NotContains(t, out, "older than")
				require.NoError(t, err)
			}
		})
	}
}

func TestDoctorCommandFindsNoClientTooOldWhenTheServerNamesNoOldest(t *testing.T) {
	setConfigEnv(t)
	withVersion(t, "v0.0.9")
	s := serveFake(t)
	s.health = api.Health{Status: "ok"}

	out, err := run(t, "doctor")
	require.NoError(t, err, "a server from before minClientVersion turns no client away")
	require.Contains(t, out, "versions: client v0.0.9, server unknown\n")
	require.NotContains(t, out, "older than")
	require.Contains(t, out, "nodes: 0, edges: 0, events: 0\n")
}

func TestVersionBelowComparesReleaseNumbersOnly(t *testing.T) {
	tests := []struct {
		v, min string
		want   bool
	}{
		{"v1.1.0", "v1.2.0", true},
		{"v1.2.0", "v1.2.0", false},
		{"v1.10.0", "v1.9.0", false},
		{"v0.9.9", "v1.0.0", true},
		{"v1.1.0-2-ge114c52", "v1.1.0", false},
		{"dev", "v1.2.0", false},
		{"v1.0.0", "", false},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, versionBelow(tt.v, tt.min), "%s below %s", tt.v, tt.min)
	}
}

func TestDoctorCommandNamesTheKeyItsUserAndItsWorkspace(t *testing.T) {
	setConfigEnv(t)
	serveFake(t)

	out, err := run(t, "doctor")
	require.NoError(t, err)
	require.Regexp(t, `key: test \(bk_[A-Za-z0-9_-]{8}\)\n`, out)
	require.Contains(t, out, "user: test <test@example.com>\n")
	require.Contains(t, out, "versions: client dev, server unknown\n", "a server that names no version")
	require.Contains(t, out, "workspace: A (organization)\n")
	require.Contains(t, out, "nodes: 0, edges: 0, events: 0\n", "an admin reads the counts")
}

func asMember(s *fakeServer) {
	s.me = api.Me{
		Key:       api.Key{Prefix: "b0b1aptp", Name: "bob laptop"},
		User:      api.User{Email: "bob@example.com", Name: "Bob"},
		Workspace: api.Workspace{Key: "org-a", Name: "A", Kind: "organization", Role: api.RoleMember},
	}
}

func TestDoctorCommandGivesAMemberNoCounts(t *testing.T) {
	setConfigEnv(t)
	s := serveFake(t)
	asMember(s)

	out, err := run(t, "doctor")
	require.NoError(t, err, "a member's doctor run is not a failure")
	require.Contains(t, out, "key: bob laptop (bk_b0b1aptp)\n")
	require.Contains(t, out, "user: Bob <bob@example.com>\n")
	require.Contains(t, out, "counts: only an admin of the workspace reads them\n")
	require.NotContains(t, out, "nodes:")
	require.Zero(t, s.requested("GET /v1/doctor"), "a member does not ask for counts the server would refuse")
}

func TestDoctorCommandFailsForAMemberWhoseClientIsTooOld(t *testing.T) {
	setConfigEnv(t)
	withVersion(t, "v0.0.9")
	s := serveFake(t)
	s.health = api.Health{Status: "ok", MinClientVersion: minClientVersion}
	asMember(s)

	out, err := run(t, "doctor")
	require.ErrorContains(t, err, "doctor found problems", "a member reads no counts but still runs a client too old")
	require.Contains(t, out, "this client is older than "+minClientVersion)
	require.Contains(t, out, "counts: only an admin of the workspace reads them\n")
}

func TestDoctorCommandSaysWhenTheServerRefusesTheKey(t *testing.T) {
	setConfigEnv(t)
	s := serveFake(t)
	t.Setenv("BELLEK_TOKEN", api.KeyPrefix+"not-a-key")

	out, err := run(t, "doctor")
	require.EqualError(t, err, "the server refused BELLEK_TOKEN: the key is unknown, revoked or expired")
	require.Contains(t, out, "server: ok ("+s.URL+")\n", "the server is reachable, only the key is wrong")
	require.NotContains(t, out, "key:")
}

func TestDoctorWarnsHalfWayToTheRetentionTrigger(t *testing.T) {
	var below, above strings.Builder
	printDoctor(&below, api.DoctorReport{MaxProjectEvents: api.RetentionTrigger / 2, MaxEventsProject: "bellek", RetainableEvents: 12})
	require.Contains(t, below.String(), "largest project: bellek with 500000 events\n")
	require.Contains(t, below.String(), "retainable events: 12\n")
	require.NotContains(t, below.String(), "retention trigger")

	printDoctor(&above, api.DoctorReport{MaxProjectEvents: api.RetentionTrigger/2 + 1, MaxEventsProject: "bellek"})
	require.Contains(t, above.String(), "project bellek has 500001 events, retention trigger is one million\n")
}

func TestExportCommandWritesTheFilesAndSaysHowMany(t *testing.T) {
	setConfigEnv(t)
	resetFlags(t, exportCmd)
	s := serveFake(t)
	s.exports["demo"] = tasks(1)
	dir := t.TempDir()

	out, err := run(t, "export", "--project", "demo", "--out", dir)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("wrote 1 files to %s\n", dir), out)
	require.FileExists(t, filepath.Join(dir, "tasks", "t000.md"))
}

func TestExportReportWritesOneMarkdownFile(t *testing.T) {
	setConfigEnv(t)
	resetFlags(t, exportCmd)
	s := serveFake(t)
	report := "# Status report: Refresh auth\n\n## What\n\nRefresh auth\n\nRotate tokens.\n"
	s.reports["demo/task/t1"] = report
	s.reports["demo/session/session-0123"] = "# Status report: claudeCode a\n"
	dir := filepath.Join(t.TempDir(), "reports")

	out, err := run(t, "export", "--project", "demo", "--out", dir, "--report", "t1")
	require.NoError(t, err)
	path := filepath.Join(dir, "t1-report.md")
	require.Equal(t, "wrote "+path+"\n", out)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "a report holds captured text, like a full export")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, report, string(raw), "the file is the report the server wrote")

	_, err = run(t, "export", "--project", "demo", "--out", dir, "--report", "missing")
	require.Error(t, err)

	out, err = run(t, "export", "--project", "demo", "--out", dir, "--report", "session-0123", "--kind", "session")
	require.NoError(t, err)
	path = filepath.Join(dir, "session-0123-report.md")
	require.Equal(t, "wrote "+path+"\n", out)
	raw, err = os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(raw), "# Status report: "))
}

func TestExportRefusesKindWithoutReport(t *testing.T) {
	setConfigEnv(t)
	resetFlags(t, exportCmd)
	s := serveFake(t)
	_, err := run(t, "export", "--project", "demo", "--out", t.TempDir(), "--kind", "session")
	require.ErrorContains(t, err, "needs --report")
	require.Zero(t, s.requested("GET /v1/projects/demo/export"), "the flags are checked before any request goes out")
}

func TestImportCommandReportsWhatItChanged(t *testing.T) {
	setConfigEnv(t)
	resetFlags(t, importCmd)
	serveFake(t)
	from := t.TempDir()
	doc := "---\nkind: task\nkey: t1\ntitle: T1\nstatus: todo\n---\n\nbody\n"
	require.NoError(t, os.WriteFile(filepath.Join(from, "t1.md"), []byte(doc), 0o644))

	out, err := run(t, "import", "--project", "demo", "--from", from)
	require.NoError(t, err)
	require.Equal(t, "created 1, updated 0, unchanged 0, links 0\n", out)

	out, err = run(t, "import", "--project", "demo", "--from", from)
	require.NoError(t, err)
	require.Equal(t, "created 0, updated 0, unchanged 1, links 0\n", out)
}

func TestMCPCommandBridgesTheServerAndLeavesStdoutToTheProtocol(t *testing.T) {
	setConfigEnv(t)
	s := serveFake(t)
	empty, err := os.Open(os.DevNull)
	require.NoError(t, err)
	t.Cleanup(func() { _ = empty.Close() })
	stdin := os.Stdin
	os.Stdin = empty
	t.Cleanup(func() { os.Stdin = stdin })

	stdout, stderr := captureStd(t, func() {
		_, err := run(t, "mcp")
		require.NoError(t, err)
	})
	require.Empty(t, stdout, "the bridge was asked nothing, so it wrote nothing on the protocol stream")
	require.Contains(t, stderr, "mcp bridge ready")
	require.Contains(t, stderr, s.URL)
	require.NotZero(t, s.requested("POST /mcp"), "the bridge read the server's tools with the key")
}

func TestExportCommandWritesEveryPage(t *testing.T) {
	setConfigEnv(t)
	resetFlags(t, exportCmd)
	s := serveFake(t)
	s.exports["demo"] = tasks(205)
	dir := t.TempDir()

	out, err := run(t, "export", "--project", "demo", "--out", dir)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("wrote 205 files to %s\n", dir), out)
	require.FileExists(t, filepath.Join(dir, "tasks", "t204.md"), "the last page is written too")
	require.Equal(t, 2, s.requested("GET /v1/projects/demo/export"))
}

func TestImportCommandSendsBatchesTheServerAccepts(t *testing.T) {
	setConfigEnv(t)
	resetFlags(t, importCmd)
	s := serveFake(t)
	blob := strings.Repeat("x", 40<<10)
	from := writeDocs(t, 450, func(i int) string {
		props := ""
		if i < 40 {
			props = "props:\n  blob: " + blob + "\n"
		}
		return fmt.Sprintf("---\nkind: note\nkey: n%03d\ntitle: N\n%s---\n", i, props)
	})

	out, err := run(t, "import", "--project", "demo", "--from", from)
	require.NoError(t, err, "450 docs, 40 of them 40 KiB, are over one request by count and by size")
	require.Equal(t, "created 450, updated 0, unchanged 0, links 0\n", out)
	require.Greater(t, s.requested("POST /v1/projects/demo/import"), 2)
}

func TestExportCommandFailsWhenItCannotWriteAPage(t *testing.T) {
	setConfigEnv(t)
	resetFlags(t, exportCmd)
	s := serveFake(t)
	s.exports["demo"] = tasks(1)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tasks"), []byte("a file where the directory goes"), 0o600))

	out, err := run(t, "export", "--project", "demo", "--out", dir)
	require.ErrorContains(t, err, "tasks")
	require.NotContains(t, out, "wrote", "a page that was not written is not reported as done")
}

func TestDoctorCommandWarnsAboutRetentionAndStillSucceeds(t *testing.T) {
	setConfigEnv(t)
	s := serveFake(t)
	s.doctor = api.DoctorReport{MaxProjectEvents: 750000, MaxEventsProject: "bellek", RetainableEvents: 9}

	out, err := run(t, "doctor")
	require.NoError(t, err, "the retention warning is not a problem")
	require.Contains(t, out, "largest project: bellek with 750000 events\n")
	require.Contains(t, out, "retainable events: 9\n")
	require.Contains(t, out, "project bellek has 750000 events, retention trigger is one million\n")
}
