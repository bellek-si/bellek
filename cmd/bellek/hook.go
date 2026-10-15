package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/client"
	"github.com/bellek-si/bellek/internal/config"
	"github.com/bellek-si/bellek/internal/gitinfo"
	"github.com/bellek-si/bellek/internal/redact"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const (
	hookTimeout     = time.Second
	startTimeout    = 4 * time.Second
	hookInputMaxLen = 1 << 20
	gitBudget       = 300 * time.Millisecond
	transcriptTail  = 64 << 10
)

var hookEvents = []string{"session-start", "session-end", "pre-compact", "prompt", "pre-tool-use", "tool-use", "stop", "cwd"}

type hookInput struct {
	SessionID            string          `json:"session_id"`
	Cwd                  string          `json:"cwd"`
	Source               string          `json:"source"`
	Trigger              string          `json:"trigger"`
	Reason               string          `json:"reason"`
	Prompt               string          `json:"prompt"`
	ToolName             string          `json:"tool_name"`
	ToolUseID            string          `json:"tool_use_id"`
	ToolInput            json.RawMessage `json:"tool_input"`
	LastAssistantMessage string          `json:"last_assistant_message"`
	TranscriptPath       string          `json:"transcript_path"`
}

type hookAPI interface {
	SessionStart(ctx context.Context, in api.SessionStartInput) (*api.SessionStartResult, error)
	SessionEndReason(ctx context.Context, id uuid.UUID, reason string) error
	SessionLocation(ctx context.Context, id uuid.UUID, in api.SessionLocationInput) error
	Note(ctx context.Context, in api.NoteInput) (*api.Node, error)
	Prompt(ctx context.Context, id uuid.UUID, message string, ack []uuid.UUID) (*api.PromptResult, error)
	Artifacts(ctx context.Context, id uuid.UUID, paths []string) ([]api.Artifact, error)
	Progress(ctx context.Context, id uuid.UUID, in api.ProgressInput) error
	Briefing(ctx context.Context, id uuid.UUID, maxTokens int) (*api.Briefing, error)
}

type hookRunner struct {
	api    hookAPI
	state  *hookState
	cfg    *config.Config
	out    io.Writer
	logf   func(event, format string, args ...any)
	gitOps gitOps
	memory memoryLocator
}

type gitOps struct {
	remote    func(ctx context.Context, cwd string) string
	branch    func(ctx context.Context, cwd string) string
	diffStat  func(ctx context.Context, cwd string) string
	worktrees func(ctx context.Context, cwd string) []string
	status    func(ctx context.Context, cwd string) []string
}

var realGit = gitOps{remote: gitinfo.Remote, branch: gitinfo.Branch, diffStat: gitinfo.DiffStat, worktrees: gitinfo.Worktrees, status: gitinfo.Status}

// gitMainRoot is the main worktree, first in the list, because Claude Code
// keeps the memory of every worktree under the main checkout's folder.
func gitMainRoot(cwd string) string {
	if roots := bounded(context.Background(), cwd, gitinfo.Worktrees); len(roots) > 0 {
		return roots[0]
	}
	return ""
}

var hookCmd = &cobra.Command{
	Use:       "hook [" + strings.Join(hookEvents, "|") + "]",
	Short:     "Claude Code hook entry point, reads the event JSON on stdin",
	Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	ValidArgs: hookEvents,
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := newAppWith(stderrLogger)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bellek hook:", err)
			return nil
		}
		defer a.close()
		var in hookInput
		if raw, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), hookInputMaxLen)); err == nil && len(raw) > 0 {
			_ = json.Unmarshal(raw, &in)
		}
		budget := hookBudget(args[0])
		ctx, cancel := context.WithTimeout(cmd.Context(), budget)
		defer cancel()
		r := newHookRunner(a, cmd.OutOrStdout(), budget)
		if err := r.run(ctx, args[0], in); err != nil {
			a.log.Warnw("bellek hook failed", "event", args[0], "err", err)
		}
		return nil
	},
}

// hookBudget gives session start more time because its first call to a
// remote server pays a full TLS handshake, which alone can take half a second.
func hookBudget(event string) time.Duration {
	if event == "session-start" {
		return startTimeout
	}
	return hookTimeout
}

func newHookRunner(a *app, out io.Writer, budget time.Duration) *hookRunner {
	return &hookRunner{
		api:    a.client(client.WithTimeout(budget)),
		state:  defaultHookState(),
		cfg:    a.cfg,
		out:    out,
		logf:   func(event, format string, args ...any) { a.log.Warnw(fmt.Sprintf(format, args...), "event", event) },
		gitOps: realGit,
		memory: memoryLocator{configDir: claudeConfigDir(), toplevel: gitMainRoot, extra: a.cfg.MemoryDirs}.memoise(),
	}
}

func (r *hookRunner) run(ctx context.Context, event string, in hookInput) error {
	if in.SessionID == "" {
		return errors.New("session_id is required")
	}
	switch event {
	case "session-start":
		return r.sessionStart(ctx, in)
	case "session-end":
		return r.sessionEnd(ctx, in)
	case "pre-compact":
		return r.preCompact(ctx, in)
	case "prompt":
		return r.prompt(ctx, in)
	case "pre-tool-use":
		return r.preToolUse(ctx, in)
	case "tool-use":
		return r.toolUse(ctx, in)
	case "stop":
		return r.stop(ctx, in)
	case "cwd":
		return r.cwd(ctx, in)
	}
	return fmt.Errorf("unknown hook event %q", event)
}

func (r *hookRunner) sessionStart(ctx context.Context, in hookInput) error {
	if in.Source != "resume" {
		// A new or compacted context has lost the instruction and the file lines, so both come back once.
		r.state.DeleteRecall(in.SessionID)
	}
	id, live := r.state.Load(in.SessionID)
	if live && in.Source == "clear" {
		if err := r.api.SessionEndReason(ctx, id, "context cleared by claude code"); err != nil {
			r.warn("session-start", "could not end the cleared session: %v", err)
		}
		r.state.Delete(in.SessionID)
		live = false
	}
	recall := r.cfg.Recalls("briefing")
	var briefing *api.Briefing
	switch {
	case !live:
		started, err := r.newSession(ctx, in)
		if errors.Is(err, errNoRepository) {
			return nil
		}
		if err != nil {
			var unreachable *url.Error
			switch {
			case !recall:
			case errors.As(err, &unreachable) && unreachable.Timeout():
				_ = r.answer("SessionStart", fmt.Sprintf("Bellek at %s did not answer in time, so this session is not recorded.", r.cfg.ServerURL))
			case errors.As(err, &unreachable):
				_ = r.answer("SessionStart", fmt.Sprintf("Bellek is not reachable at %s, so this session is not recorded.", r.cfg.ServerURL))
			case client.IsCode(err, api.CodeUnauthorized):
				_ = r.answer("SessionStart", r.refused())
			}
			return err
		}
		id, briefing = started.Session.ID, &started.Briefing
	case recall:
		b, err := r.api.Briefing(ctx, id, r.cfg.BriefTokens)
		if sessionGone(err) {
			started, err := r.restart(ctx, in)
			if errors.Is(err, errNoRepository) {
				return nil
			}
			if err != nil {
				return err
			}
			id, b = started.Session.ID, &started.Briefing
		} else if err != nil {
			r.warn("session-start", "briefing not read: %v", err)
		}
		briefing = b
	}
	switch {
	case recall:
		if err := r.answer("SessionStart", briefingContext(id, briefing)); err != nil {
			return err
		}
	case briefing != nil && briefing.Notice != "":
		if err := r.answer("SessionStart", briefing.Notice); err != nil {
			return err
		}
	}
	r.drain(ctx)
	r.state.PruneSnapshots()
	if r.cfg.Captures("memory") {
		r.walkMemory(ctx, id, in.Cwd)
	}
	return nil
}

var errNoRepository = errors.New("not a git repository")

func (r *hookRunner) newSession(ctx context.Context, in hookInput) (*api.SessionStartResult, error) {
	host, _ := os.Hostname()
	loc := r.location(ctx, in.Cwd)
	if loc.RepoRoot == "" && loc.RepoRemote == "" {
		return nil, errNoRepository
	}
	harness, _ := api.CanonicalUUID(in.SessionID)
	started, err := r.api.SessionStart(ctx, api.SessionStartInput{Cwd: loc.Cwd, RepoRemote: loc.RepoRemote, RepoRoot: loc.RepoRoot, AgentKind: "claudeCode", AgentID: host, Branch: loc.Branch, MaxTokens: r.cfg.BriefTokens, HarnessSessionID: harness})
	if err != nil {
		return nil, err
	}
	return started, r.state.Save(in.SessionID, started.Session.ID)
}

func (r *hookRunner) restart(ctx context.Context, in hookInput) (*api.SessionStartResult, error) {
	r.state.Delete(in.SessionID)
	r.state.DeleteRecall(in.SessionID)
	return r.newSession(ctx, in)
}

// Without a remote the server resolves the project by path, so every worktree reports the main one.
func (r *hookRunner) location(ctx context.Context, cwd string) api.SessionLocationInput {
	loc := api.SessionLocationInput{
		Cwd:        redact.Text(cwd, 0),
		RepoRemote: redact.Text(r.remote(ctx, cwd), 0),
		Branch:     redact.Text(r.branch(ctx, cwd), 0),
	}
	if roots := r.worktrees(ctx, cwd); len(roots) > 0 {
		loc.Cwd = redact.Text(roots[0], 0)
		loc.RepoRoot = loc.Cwd
	}
	return loc
}

func (r *hookRunner) cwd(ctx context.Context, in hookInput) error {
	id, ok := r.state.Load(in.SessionID)
	if !ok {
		return nil
	}
	if err := r.api.SessionLocation(ctx, id, r.location(ctx, in.Cwd)); err != nil {
		r.warn("cwd", "location not recorded: %v", err)
	}
	return nil
}

func (r *hookRunner) sessionEnd(ctx context.Context, in hookInput) error {
	id, ok := r.state.Load(in.SessionID)
	if !ok {
		r.state.Delete(in.SessionID)
		return nil
	}
	r.drain(ctx)
	if r.cfg.Captures("progress") {
		if diff := r.diffStat(ctx, in.Cwd); diff != "" {
			if err := r.sendProgress(ctx, "session-end", id, in, redact.Text(api.DiffStatPrefix+diff, api.MaxMessageBytes)); err != nil {
				r.warn("session-end", "final progress not recorded: %v", err)
			}
		}
	}
	// The mapping outlives a failed end call so a resume finds the session still open on the server.
	if err := r.api.SessionEndReason(ctx, id, endSummary(in.Reason)); err != nil && !rejected(err) {
		return err
	}
	r.state.Delete(in.SessionID)
	return nil
}

func (r *hookRunner) preCompact(ctx context.Context, in hookInput) error {
	id, ok := r.state.Load(in.SessionID)
	if !ok {
		return nil
	}
	r.drain(ctx)
	var body strings.Builder
	fmt.Fprintf(&body, "Context compaction (%s) at %s.\n", in.Trigger, time.Now().UTC().Format(time.RFC3339))
	if branch := r.branch(ctx, in.Cwd); branch != "" {
		fmt.Fprintf(&body, "\nBranch: %s\n", branch)
	}
	if diff := r.diffStat(ctx, in.Cwd); diff != "" {
		fmt.Fprintf(&body, "\n```\ngit diff --stat\n%s\n```\n", diff)
	}
	note := api.NoteInput{SessionID: id, Title: "Checkpoint before compaction", Tags: []string{"checkpoint"}, Body: redact.Text(body.String(), api.MaxBodyBytes)}
	if _, err := r.api.Note(ctx, note); err != nil {
		r.spool(spooled{Op: "note", SessionID: id, Note: &note}, err)
	}
	return nil
}

func (r *hookRunner) prompt(ctx context.Context, in hookInput) error {
	id, ok := r.state.Load(in.SessionID)
	if !ok || harnessMessage(in.Prompt) {
		return nil
	}
	var res *api.PromptResult
	marker := r.state.Recalled(in.SessionID)
	first := !marker.Instructed
	message := redact.Text(in.Prompt, api.MaxMessageBytes)
	if r.cfg.Captures("prompts") && strings.TrimSpace(message) != "" {
		r.drain(ctx)
		got, err := r.api.Prompt(ctx, id, message, marker.Signals)
		if sessionGone(err) {
			started, startErr := r.restart(ctx, in)
			if errors.Is(startErr, errNoRepository) {
				return nil
			}
			if startErr != nil {
				r.warn("prompt", "session ended and none started: %v", startErr)
				return nil
			}
			id, marker, first = started.Session.ID, recallMarker{}, true
			got, err = r.api.Prompt(ctx, id, message, nil)
		}
		if client.IsCode(err, api.CodeUnauthorized) {
			r.warn("prompt", "capture rejected: %v", err)
			r.state.Delete(in.SessionID)
			if r.cfg.Recalls("prompt") {
				return r.answer("UserPromptSubmit", r.refused())
			}
			return nil
		}
		if err != nil {
			r.spool(spooled{Op: "prompt", SessionID: id, Message: message}, err)
		} else {
			res = got
		}
	}
	if err := r.answer("UserPromptSubmit", promptContext(id, res, r.cfg.Recalls("prompt"), first)); err != nil {
		return err
	}
	marker.Instructed = true
	marker.Signals = nil
	if res != nil && r.cfg.Recalls("prompt") {
		for _, sig := range res.Signals {
			marker.Signals = append(marker.Signals, sig.ID)
		}
	}
	if err := r.state.SaveRecalled(in.SessionID, marker); err != nil {
		r.warn("prompt", "recall marker not written: %v", err)
	}
	return nil
}

func (r *hookRunner) toolUse(ctx context.Context, in hookInput) error {
	id, ok := r.state.Load(in.SessionID)
	if !ok {
		return nil
	}
	if r.cfg.Captures("memory") {
		if paths := memoryPaths(in.ToolInput, r.memory.isMemoryFile); len(paths) > 0 {
			r.drain(ctx)
			for _, p := range paths {
				r.syncMemory(ctx, id, p)
			}
		}
	}
	if !r.cfg.Captures("files") {
		return nil
	}
	roots := func() []string { return r.worktrees(ctx, in.Cwd) }
	var paths []string
	if in.ToolName == "Bash" {
		paths = relativePaths(r.bashChanges(ctx, in), in.Cwd, r.cfg.CaptureIgnore, r.memory.isMemoryFile, roots)
	} else {
		paths = touchedPaths(in.ToolName, in.ToolInput, in.Cwd, r.cfg.CaptureIgnore, r.memory.isMemoryFile, roots)
	}
	if len(paths) == 0 {
		return nil
	}
	r.drain(ctx)
	items, err := r.api.Artifacts(ctx, id, paths)
	if err != nil {
		r.spool(spooled{Op: "artifacts", SessionID: id, Paths: paths}, err)
		return nil
	}
	if !r.cfg.Recalls("files") {
		return nil
	}
	return r.recallFiles(in.SessionID, items)
}

func (r *hookRunner) recallFiles(claudeID string, items []api.Artifact) error {
	marker := r.state.Recalled(claudeID)
	var fresh []api.Artifact
	for _, a := range items {
		if (len(a.Links) == 0 && len(a.Sessions) == 0) || slices.Contains(marker.Files, a.Key) {
			continue
		}
		fresh = append(fresh, a)
		marker.Files = append(marker.Files, a.Key)
	}
	if len(fresh) == 0 {
		return nil
	}
	if err := r.state.SaveRecalled(claudeID, marker); err != nil {
		r.warn("tool-use", "recall marker not written: %v", err)
	}
	return r.answer("PostToolUse", fileContext(fresh))
}

func (r *hookRunner) stop(ctx context.Context, in hookInput) error {
	id, ok := r.state.Load(in.SessionID)
	if !ok || !r.cfg.Captures("progress") {
		return nil
	}
	message := redact.Text(in.LastAssistantMessage, api.MaxMessageBytes)
	if strings.TrimSpace(message) == "" {
		return nil
	}
	r.drain(ctx)
	if err := r.sendProgress(ctx, "stop", id, in, message); err != nil {
		r.spool(spooled{Op: "progress", SessionID: id, Message: message}, err)
	}
	return nil
}

func endSummary(reason string) string {
	if reason == "" {
		return "session ended by claude code"
	}
	return "session ended by claude code: " + reason
}

func (r *hookRunner) sendProgress(ctx context.Context, event string, id uuid.UUID, in hookInput, message string) error {
	name := redact.Text(transcriptName(in.TranscriptPath), 0)
	if name == r.state.LoadName(in.SessionID) {
		name = ""
	}
	if err := r.api.Progress(ctx, id, api.ProgressInput{Message: message, Name: name}); err != nil {
		return err
	}
	if name != "" {
		if err := r.state.SaveName(in.SessionID, name); err != nil {
			r.warn(event, "session name not remembered: %v", err)
		}
	}
	return nil
}

// transcriptName is the name Claude Code shows for the session. customTitle is set by /rename,
// and agentName wins over the generated aiTitle because it is the name SendMessage takes.
func transcriptName(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	if size := info.Size(); size > transcriptTail {
		if _, err := f.Seek(size-transcriptTail, io.SeekStart); err != nil {
			return ""
		}
	}
	raw, err := io.ReadAll(io.LimitReader(f, transcriptTail))
	if err != nil {
		return ""
	}
	var agentName, aiTitle, slug string
	lines := strings.Split(string(raw), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var entry struct {
			CustomTitle string `json:"customTitle"`
			AgentName   string `json:"agentName"`
			AITitle     string `json:"aiTitle"`
			Slug        string `json:"slug"`
		}
		if json.Unmarshal([]byte(lines[i]), &entry) != nil {
			continue
		}
		if entry.CustomTitle != "" {
			return entry.CustomTitle
		}
		agentName = cmp.Or(agentName, entry.AgentName)
		aiTitle = cmp.Or(aiTitle, entry.AITitle)
		slug = cmp.Or(slug, entry.Slug)
	}
	return cmp.Or(agentName, aiTitle, slug)
}

func bounded[T any](ctx context.Context, cwd string, query func(context.Context, string) T) T {
	var zero T
	if query == nil {
		return zero
	}
	ctx, cancel := context.WithTimeout(ctx, gitBudget)
	defer cancel()
	return query(ctx, cwd)
}

func (r *hookRunner) remote(ctx context.Context, cwd string) string {
	return bounded(ctx, cwd, r.gitOps.remote)
}

func (r *hookRunner) branch(ctx context.Context, cwd string) string {
	return bounded(ctx, cwd, r.gitOps.branch)
}

func (r *hookRunner) diffStat(ctx context.Context, cwd string) string {
	return bounded(ctx, cwd, r.gitOps.diffStat)
}

func (r *hookRunner) worktrees(ctx context.Context, cwd string) []string {
	return bounded(ctx, cwd, r.gitOps.worktrees)
}

func (r *hookRunner) status(ctx context.Context, cwd string) []string {
	return bounded(ctx, cwd, r.gitOps.status)
}

func (r *hookRunner) send(ctx context.Context, item spooled) error {
	switch item.Op {
	case "prompt":
		_, err := r.api.Prompt(ctx, item.SessionID, item.Message, nil)
		return err
	case "artifacts":
		_, err := r.api.Artifacts(ctx, item.SessionID, item.Paths)
		return err
	case "progress":
		return r.api.Progress(ctx, item.SessionID, api.ProgressInput{Message: item.Message})
	case "note":
		if item.Note == nil {
			return nil
		}
		_, err := r.api.Note(ctx, *item.Note)
		return err
	}
	return nil
}

func (r *hookRunner) drain(ctx context.Context) {
	send := func(item spooled) error {
		err := r.send(ctx, item)
		if rejected(err) {
			r.warn(item.Op, "spooled capture dropped: %v", err)
			return nil
		}
		return err
	}
	if _, err := r.state.Drain(send); err != nil {
		r.warn("drain", "spool replay stopped: %v", err)
	}
}

func (r *hookRunner) spool(item spooled, cause error) {
	if rejected(cause) {
		r.warn(item.Op, "capture rejected: %v", cause)
		return
	}
	if err := r.state.Spool(item); err != nil {
		r.warn(item.Op, "capture lost, spool failed: %v (cause %v)", err, cause)
		return
	}
	r.warn(item.Op, "capture spooled: %v", cause)
}

func sessionGone(err error) bool {
	var e *client.Error
	return errors.As(err, &e) && (e.Code == api.CodeSessionEnded || e.Code == api.CodeNotFound && e.Message == "session not found")
}

// rejected reports an answer from the server that a retry would get again. A
// refused key is one: the capture must not wait for a new key, which may
// belong to another workspace.
func rejected(err error) bool {
	return client.IsCode(err, api.CodeValidation) || client.IsCode(err, api.CodeSessionEnded) || client.IsCode(err, api.CodeNotFound) ||
		client.IsCode(err, api.CodeUnauthorized)
}

func (r *hookRunner) refused() string {
	return fmt.Sprintf("Bellek at %s refused BELLEK_TOKEN, so this session is not recorded. The key may be revoked or expired; create a new one on the settings page.", r.cfg.ServerURL)
}

func (r *hookRunner) warn(event, format string, args ...any) {
	if r.logf != nil {
		r.logf(event, format, args...)
	}
}

func (r *hookRunner) answer(event, context string) error {
	if context == "" {
		return nil
	}
	out := map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": event, "additionalContext": context}}
	return json.NewEncoder(r.out).Encode(out)
}

func init() {
	rootCmd.AddCommand(hookCmd)
}
