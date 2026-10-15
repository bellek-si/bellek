package api

import "github.com/google/uuid"

type SessionStartInput struct {
	Cwd        string `json:"cwd,omitempty"`
	ProjectKey string `json:"projectKey,omitempty"`
	RepoRemote string `json:"repoRemote,omitempty"`
	RepoRoot   string `json:"repoRoot,omitempty"`
	AgentKind  string `json:"agentKind"`
	AgentID    string `json:"agentId,omitempty"`
	Branch     string `json:"branch,omitempty"`
	MaxTokens  int    `json:"maxTokens,omitempty"`
	// HarnessSessionID resumes the session in the harness that started it.
	HarnessSessionID string `json:"harnessSessionId,omitempty"`
}

type Session struct {
	ID        uuid.UUID      `json:"id"`
	Key       string         `json:"key"`
	ProjectID uuid.UUID      `json:"projectId"`
	Status    string         `json:"status"`
	Props     map[string]any `json:"props"`
}

type Briefing struct {
	Markdown  string `json:"markdown"`
	Truncated bool   `json:"truncated"`
	// Notice is one line the server adds while the workspace's free plan is full.
	Notice string `json:"notice,omitempty"`
}

type SessionStartResult struct {
	Session  Session  `json:"session"`
	Briefing Briefing `json:"briefing"`
}

type SessionEndInput struct {
	Summary string `json:"summary,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type SessionLocationInput struct {
	Cwd        string `json:"cwd"`
	RepoRemote string `json:"repoRemote,omitempty"`
	RepoRoot   string `json:"repoRoot,omitempty"`
	Branch     string `json:"branch,omitempty"`
}

type PromptInput struct {
	Message    string      `json:"message"`
	AckSignals []uuid.UUID `json:"ackSignals,omitempty"`
}

type Signal struct {
	ID             uuid.UUID `json:"id"`
	FromSessionKey string    `json:"fromSessionKey"`
	FromFocusKey   string    `json:"fromFocusKey"`
	FromFocus      string    `json:"fromFocus,omitempty"`
	Kind           string    `json:"kind"`
	ThreadID       string    `json:"threadId"`
	Message        string    `json:"message"`
}

type PromptResult struct {
	Turn              int      `json:"turn"`
	PreviousTurnFiles int      `json:"previousTurnFiles"`
	PreviousTurnNoted bool     `json:"previousTurnNoted"`
	Related           []Ref    `json:"related"`
	Signals           []Signal `json:"signals"`
	Focus             *Ref     `json:"focus,omitempty"`
	FocusTurn         int      `json:"focusTurn,omitempty"`
}

type ArtifactsInput struct {
	Paths []string `json:"paths"`
}

type SessionRef struct {
	Key        string `json:"key"`
	FocusKey   string `json:"focusKey"`
	Focus      string `json:"focus,omitempty"`
	AgentID    string `json:"agentId"`
	Branch     string `json:"branch"`
	AgoSeconds int    `json:"agoSeconds"`
}

type Artifact struct {
	Key      string       `json:"key"`
	Title    string       `json:"title"`
	Links    []Ref        `json:"links"`
	Sessions []SessionRef `json:"sessions"`
}

// ProgressInput carries a turn's closing message and, when it changed, the name the
// harness gave the session.
type ProgressInput struct {
	Message string `json:"message"`
	Name    string `json:"name,omitempty"`
}
