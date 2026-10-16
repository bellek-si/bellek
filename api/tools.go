package api

// Instructions is what the MCP server sends a client when it connects.
const Instructions = "Bellek is the shared memory for this project. With the Bellek hooks installed the session is already started and its briefing is already in context; the session id is in that context. Search with memory_search or memory_get before touching a task or a file. When the user asks for a piece of work, or you pull tasks from a tracker such as Linear or Jira, create each one with task_upsert, link related work with parentKey and dependsOn, and claim the one you start with task_claim, which returns its briefing; a claim leaves the status alone, so move a todo task to inProgress with task_status. Do all three before your first edit, even for a small one line request. Every task has an id, the project key and a number such as widgets-12; use it when you name a task to the user, and pass it wherever a task key goes. Record decisions, lessons and open questions with memory_note. Move the task with task_status: done when finished, review when it needs a look, blocked with the reason, cancelled when dropped; task_handoff when you stop mid task. When you move a task to done or review, write a memory_note tagged report with key <task key>-report and relatedKey the task, with the headings ## What, ## Why, ## Changes and ## Conclusion. Other sessions are listed in the briefing with their key and focus; a live Claude Code peer is reached with SendMessage by its name, any other session with memory_signal to its key, and a request signal arrives on that session's next prompt; answer one with kind response and the same threadId. Call session_start, then memory_briefing, only when no briefing is in context, and session_end only if you called session_start."

// Tool is an MCP tool; ReadOnly sets its MCP read only hint.
type Tool struct {
	Name        string
	Description string
	ReadOnly    bool
}

var (
	ToolSessionStart = Tool{Name: "session_start", Description: "Start a session and get the briefing. Call this first."}
	ToolSessionEnd   = Tool{Name: "session_end", Description: "End the session, hand off claimed tasks, release leases."}
	ToolBriefing     = Tool{Name: "memory_briefing", Description: "What matters now: your claims, blocked tasks, signals, handoffs, decisions, frontier, other sessions.", ReadOnly: true}
	ToolGet          = Tool{Name: "memory_get", Description: "Get a node with its neighbors and recent events.", ReadOnly: true}
	ToolSearch       = Tool{Name: "memory_search", Description: "Full text search with one hop of related nodes.", ReadOnly: true}
	ToolNote         = Tool{Name: "memory_note", Description: "Write a note or decision. It is linked to related work automatically. The user may later accept, discard, answer or promote it, and a discarded note drops out of search and the briefing."}
	ToolSignal       = Tool{Name: "memory_signal", Description: "Send a message to another session or attach it to a task."}
	ToolTaskUpsert   = Tool{Name: "task_upsert", Description: "Create or update a task. Create one for each piece of work the user asks for or you pull from a tracker, before your first edit, with parentKey and dependsOn for related work."}
	ToolTaskStatus   = Tool{Name: "task_status", Description: "Change a task status following the state machine: done when finished, review when it needs a look, blocked with the reason, cancelled when dropped. At done or review, also write a memory_note tagged report with key <task key>-report and the headings ## What, ## Why, ## Changes and ## Conclusion."}
	ToolTaskClaim    = Tool{Name: "task_claim", Description: "Claim, release or renew the lease on a task. A claim makes the task your focus and returns its briefing; it does not change the status, so move a todo task to inProgress with task_status."}
	ToolTaskHandoff  = Tool{Name: "task_handoff", Description: "Hand a task off with summary, open questions, next steps and files touched."}
	ToolTaskNext     = Tool{Name: "task_next", Description: "Unblocked, unclaimed tasks ordered by priority.", ReadOnly: true}
)

var Tools = []Tool{
	ToolSessionStart, ToolSessionEnd, ToolBriefing, ToolGet, ToolSearch, ToolNote, ToolSignal,
	ToolTaskUpsert, ToolTaskStatus, ToolTaskClaim, ToolTaskHandoff, ToolTaskNext,
}

type ScopeArgs struct {
	ProjectKey string `json:"projectKey,omitempty" jsonschema:"explicit project key, overrides repoRemote"`
	RepoRemote string `json:"repoRemote,omitempty" jsonschema:"git remote url or address of the repository, filled by the bridge"`
}

type SessionArgs struct {
	SessionID string `json:"sessionId" jsonschema:"session id returned by session_start"`
}

func (a SessionArgs) Session() string { return a.SessionID }

type BriefingArgs struct {
	SessionArgs
	MaxTokens int `json:"maxTokens,omitempty" jsonschema:"token budget, default 2000"`
}

type GetArgs struct {
	ScopeArgs
	Key   string `json:"key" jsonschema:"node key, or a task id such as widgets-12"`
	Kind  Kind   `json:"kind,omitempty" jsonschema:"task, note, decision, session or project, default task"`
	Depth int    `json:"depth,omitempty" jsonschema:"neighbor depth 1 to 3, default 1"`
	AsOf  string `json:"asOf,omitempty" jsonschema:"RFC3339 time to rebuild task status as of"`
}

type SearchArgs struct {
	ScopeArgs
	Q       string   `json:"q" jsonschema:"search text"`
	Kinds   []string `json:"kinds,omitempty" jsonschema:"restrict text hits to these kinds; session finds who worked on something"`
	Limit   int      `json:"limit,omitempty" jsonschema:"max text hits, default 20, at most 100"`
	After   string   `json:"after,omitempty" jsonschema:"RFC3339, only nodes updated after this time"`
	Before  string   `json:"before,omitempty" jsonschema:"RFC3339, only nodes updated before this time"`
	Explain bool     `json:"explain,omitempty" jsonschema:"include the rank, via and edge score fields"`
}

type NoteArgs struct {
	SessionArgs
	Key         string         `json:"key,omitempty" jsonschema:"stable key, generated from the title when empty"`
	Kind        Kind           `json:"kind,omitempty" jsonschema:"note or decision, default note"`
	Title       string         `json:"title" jsonschema:"short title"`
	Body        string         `json:"body,omitempty" jsonschema:"markdown body"`
	Tags        []string       `json:"tags,omitempty"`
	Props       map[string]any `json:"props,omitempty" jsonschema:"kind specific fields, camelCase keys, merged on update"`
	RelatedKey  string         `json:"relatedKey,omitempty" jsonschema:"task id, task key or note key this note is about"`
	BaseVersion int            `json:"baseVersion,omitempty" jsonschema:"version this edit was based on, refused with conflict when the latest moved on; leave empty"`
	ProjectKey  string         `json:"projectKey,omitempty" jsonschema:"global for your own rule in every repository, team-<team key> or org to propose a rule to a team or the whole org with the tag proposed (an admin writes it outright); leave empty for this project"`
	Visibility  Visibility     `json:"visibility,omitempty" jsonschema:"private or shared; a new decision or report is shared with everyone in the project, any other new note is private to you and the admins above you; leave empty to keep it"`
}

type SignalArgs struct {
	SessionArgs
	To       string     `json:"to" jsonschema:"target session id, session key, task id or task key; a request reaches the session on its next prompt"`
	Kind     SignalKind `json:"kind" jsonschema:"info, request, response or alert"`
	Message  string     `json:"message" jsonschema:"at most 200 characters"`
	ThreadID string     `json:"threadId,omitempty" jsonschema:"answer a request with kind response and the same threadId"`
}

type TaskUpsertArgs struct {
	SessionArgs
	Key         string         `json:"key" jsonschema:"kebab case key for a new task, or the id or key of an existing one"`
	Title       string         `json:"title"`
	Body        string         `json:"body,omitempty"`
	Props       map[string]any `json:"props,omitempty" jsonschema:"priority 1 to 5, owner, acceptance"`
	ParentKey   string         `json:"parentKey,omitempty" jsonschema:"id or key of the parent task"`
	DependsOn   []string       `json:"dependsOn,omitempty" jsonschema:"ids or keys of the tasks this task waits for"`
	BaseVersion int            `json:"baseVersion,omitempty" jsonschema:"version this edit was based on, refused with conflict when the latest moved on; leave empty"`
}

type TaskStatusArgs struct {
	SessionArgs
	Key     string     `json:"key" jsonschema:"task id such as widgets-12, or task key"`
	Status  TaskStatus `json:"status" jsonschema:"todo, inProgress, blocked, review, done or cancelled"`
	Message string     `json:"message,omitempty" jsonschema:"why, required when blocked"`
}

type TaskClaimArgs struct {
	SessionArgs
	Key       string `json:"key" jsonschema:"task id such as widgets-12, or task key"`
	Action    string `json:"action" jsonschema:"claim, release or renew"`
	MaxTokens int    `json:"maxTokens,omitempty" jsonschema:"budget of the task briefing a claim returns, default 1200"`
}

type TaskHandoffArgs struct {
	SessionArgs
	Key           string   `json:"key" jsonschema:"task id such as widgets-12, or task key"`
	Summary       string   `json:"summary"`
	OpenQuestions []string `json:"openQuestions,omitempty"`
	NextSteps     []string `json:"nextSteps,omitempty"`
	FilesTouched  []string `json:"filesTouched,omitempty"`
}

type TaskNextArgs struct {
	ScopeArgs
	Limit int `json:"limit,omitempty" jsonschema:"default 5"`
}

type SessionStartArgs struct {
	ScopeArgs
	AgentKind        string `json:"agentKind" jsonschema:"claudeCode, codex, cursor or custom"`
	AgentID          string `json:"agentId,omitempty" jsonschema:"machine or harness name"`
	Branch           string `json:"branch,omitempty"`
	MaxTokens        int    `json:"maxTokens,omitempty" jsonschema:"briefing budget in tokens, default 2000"`
	HarnessSessionID string `json:"harnessSessionId,omitempty" jsonschema:"the harness own session id, a uuid, so the session page can print a resume command"`
}

// HandoffArgs is one handoff in session_end, which names the task taskKey where
// task_handoff names it key. Embedding one in the other would put both names in
// the schema.
type HandoffArgs struct {
	TaskKey       string   `json:"taskKey" jsonschema:"task id such as widgets-12, or task key"`
	Summary       string   `json:"summary"`
	OpenQuestions []string `json:"openQuestions,omitempty"`
	NextSteps     []string `json:"nextSteps,omitempty"`
	FilesTouched  []string `json:"filesTouched,omitempty"`
}

type SessionEndArgs struct {
	SessionArgs
	Summary  string        `json:"summary"`
	Handoffs []HandoffArgs `json:"handoffs,omitempty" jsonschema:"per task handoffs, default one per claimed task using summary"`
}
