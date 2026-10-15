package api

import (
	"time"

	"github.com/google/uuid"
)

type Kind string

const (
	KindProject  Kind = "project"
	KindTask     Kind = "task"
	KindSession  Kind = "session"
	KindAgent    Kind = "agent"
	KindNote     Kind = "note"
	KindDecision Kind = "decision"
	KindArtifact Kind = "artifact"
)

func (k Kind) Valid() bool {
	switch k {
	case KindProject, KindTask, KindSession, KindAgent, KindNote, KindDecision, KindArtifact:
		return true
	}
	return false
}

func (k Kind) Private() bool { return k == KindNote || k == KindDecision }

type Origin string

const (
	OriginUser   Origin = "user"
	OriginAgent  Origin = "agent"
	OriginHook   Origin = "hook"
	OriginImport Origin = "import"
)

func (o Origin) Valid() bool {
	switch o {
	case OriginUser, OriginAgent, OriginHook, OriginImport:
		return true
	}
	return false
}

// Visibility is set on notes and decisions. Every other kind is shared.
type Visibility string

const (
	VisibilityPrivate Visibility = "private"
	VisibilityShared  Visibility = "shared"
)

func (v Visibility) Valid() bool { return v == VisibilityPrivate || v == VisibilityShared }

type TaskStatus string

const (
	TaskTodo       TaskStatus = "todo"
	TaskInProgress TaskStatus = "inProgress"
	TaskBlocked    TaskStatus = "blocked"
	TaskReview     TaskStatus = "review"
	TaskDone       TaskStatus = "done"
	TaskCancelled  TaskStatus = "cancelled"
)

func (s TaskStatus) Valid() bool {
	switch s {
	case TaskTodo, TaskInProgress, TaskBlocked, TaskReview, TaskDone, TaskCancelled:
		return true
	}
	return false
}

type SessionStatus string

const (
	SessionActive SessionStatus = "active"
	SessionEnded  SessionStatus = "ended"
)

func (s SessionStatus) Valid() bool { return s == SessionActive || s == SessionEnded }

type EdgeType string

const (
	EdgeBelongsTo  EdgeType = "belongsTo"
	EdgeParentOf   EdgeType = "parentOf"
	EdgeDependsOn  EdgeType = "dependsOn"
	EdgeBlockedBy  EdgeType = "blockedBy"
	EdgeWorkedOnBy EdgeType = "workedOnBy"
	EdgeProduced   EdgeType = "produced"
	EdgeRelatesTo  EdgeType = "relatesTo"
	EdgeSupersedes EdgeType = "supersedes"
)

func (t EdgeType) Valid() bool {
	switch t {
	case EdgeBelongsTo, EdgeParentOf, EdgeDependsOn, EdgeBlockedBy,
		EdgeWorkedOnBy, EdgeProduced, EdgeRelatesTo, EdgeSupersedes:
		return true
	}
	return false
}

type EventType string

const (
	EventStatusChanged EventType = "statusChanged"
	EventClaimed       EventType = "claimed"
	EventReleased      EventType = "released"
	EventHandoff       EventType = "handoff"
	EventSignal        EventType = "signal"
	EventNoteAdded     EventType = "noteAdded"
	EventProgress      EventType = "progress"
	EventPrompt        EventType = "prompt"
)

func (t EventType) Valid() bool {
	switch t {
	case EventStatusChanged, EventClaimed, EventReleased, EventHandoff,
		EventSignal, EventNoteAdded, EventProgress, EventPrompt:
		return true
	}
	return false
}

// Status holds a TaskStatus or a SessionStatus, by Kind. AuthorID is the user
// who wrote the first version and keeps it across versions; CreatedBy is the
// session that wrote this one.
type Node struct {
	ID         uuid.UUID      `json:"id"`
	Kind       Kind           `json:"kind"`
	Key        string         `json:"key"`
	Number     int            `json:"number,omitempty"`
	Ref        string         `json:"ref,omitempty"`
	ProjectID  uuid.UUID      `json:"projectId"`
	Title      string         `json:"title"`
	Status     string         `json:"status"`
	Body       string         `json:"body"`
	Context    string         `json:"context"`
	Keywords   []string       `json:"keywords"`
	Tags       []string       `json:"tags"`
	Props      map[string]any `json:"props"`
	Origin     Origin         `json:"origin"`
	Version    int            `json:"version"`
	IsLatest   bool           `json:"isLatest"`
	AuthorID   uuid.UUID      `json:"authorId"`
	Visibility Visibility     `json:"visibility"`
	CreatedBy  uuid.UUID      `json:"createdBy"`
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
}

// Label is the task id and the key, such as widgets-12 auth-refresh, or the
// key alone when the node has no id.
func (n Node) Label() string { return label(n.Ref, n.Key) }

type Edge struct {
	ID        uuid.UUID `json:"id"`
	FromID    uuid.UUID `json:"fromId"`
	ToID      uuid.UUID `json:"toId"`
	ToKey     string    `json:"toKey"`
	Type      EdgeType  `json:"type"`
	Score     float64   `json:"score"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

type Event struct {
	ID        uuid.UUID      `json:"id"`
	ProjectID uuid.UUID      `json:"projectId"`
	NodeID    uuid.UUID      `json:"nodeId"`
	SessionID uuid.UUID      `json:"sessionId"`
	Type      EventType      `json:"type"`
	Message   string         `json:"message"`
	Data      map[string]any `json:"data"`
	Origin    Origin         `json:"origin"`
	CreatedAt time.Time      `json:"createdAt"`
}

type Ref struct {
	Kind   Kind   `json:"kind"`
	Key    string `json:"key"`
	Ref    string `json:"ref,omitempty"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

func (r Ref) Label() string { return label(r.Ref, r.Key) }

func label(ref, key string) string {
	if ref == "" {
		return key
	}
	return ref + " " + key
}

type NoteInput struct {
	SessionID   uuid.UUID      `json:"sessionId"`
	Key         string         `json:"key,omitempty"`
	Kind        Kind           `json:"kind,omitempty"`
	Title       string         `json:"title"`
	Body        string         `json:"body,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Props       map[string]any `json:"props,omitempty"`
	RelatedKey  string         `json:"relatedKey,omitempty"`
	BaseVersion int            `json:"baseVersion,omitempty"`
	ProjectKey  string         `json:"projectKey,omitempty"`
	Visibility  Visibility     `json:"visibility,omitempty"`
}
