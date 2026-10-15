package api

import (
	"time"

	"github.com/google/uuid"
)

// ExportCursor is where the next page starts: after the last node the page
// listed, oldest first.
type ExportCursor struct {
	After   time.Time `json:"after"`
	AfterID uuid.UUID `json:"afterId"`
}

type ExportTarget struct {
	Key  string `json:"key"`
	Kind Kind   `json:"kind"`
}

// Snapshot is one page of a project. Targets names every node its edges point
// at, so a page can be written without the pages around it.
type Snapshot struct {
	Project *Node                      `json:"project"`
	Nodes   []*Node                    `json:"nodes"`
	Edges   map[uuid.UUID][]*Edge      `json:"edges"`
	Events  map[uuid.UUID][]*Event     `json:"events"`
	Targets map[uuid.UUID]ExportTarget `json:"targets"`
	Next    *ExportCursor              `json:"next,omitempty"`
}

type ImportLink struct {
	Type EdgeType `json:"type"`
	To   string   `json:"to"`
	Kind Kind     `json:"kind,omitempty"`
}

type ImportDoc struct {
	Kind   Kind           `json:"kind"`
	Key    string         `json:"key"`
	Title  string         `json:"title"`
	Status string         `json:"status,omitempty"`
	Body   string         `json:"body,omitempty"`
	Tags   []string       `json:"tags,omitempty"`
	Props  map[string]any `json:"props,omitempty"`
	Links  []ImportLink   `json:"links,omitempty"`
	// Visibility is private or shared on a note or decision. A new one without it gets the
	// default of a note write, and an existing one keeps its own.
	Visibility Visibility `json:"visibility,omitempty"`
}

type ImportInput struct {
	Docs []ImportDoc `json:"docs"`
}

type ImportReport struct {
	Created   int    `json:"created"`
	Updated   int    `json:"updated"`
	Unchanged int    `json:"unchanged"`
	Links     int    `json:"links"`
	Skipped   int    `json:"skipped"`
	Notice    string `json:"notice,omitempty"`
}

const ImportMaxDocs = 200

type Report struct {
	Markdown    string    `json:"markdown"`
	GeneratedAt time.Time `json:"generatedAt"`
}

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

func (r Role) Valid() bool { return r == RoleAdmin || r == RoleMember }

type WorkspaceKind string

const (
	WorkspacePersonal     WorkspaceKind = "personal"
	WorkspaceOrganization WorkspaceKind = "organization"
)

func (k WorkspaceKind) Valid() bool { return k == WorkspacePersonal || k == WorkspaceOrganization }

type Me struct {
	Key       Key       `json:"key"`
	User      User      `json:"user"`
	Workspace Workspace `json:"workspace"`
}

type Key struct {
	ID        uuid.UUID `json:"id"`
	Prefix    string    `json:"prefix"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
}

type User struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Name  string    `json:"name"`
}

type Workspace struct {
	ID   uuid.UUID     `json:"id"`
	Key  string        `json:"key"`
	Name string        `json:"name"`
	Kind WorkspaceKind `json:"kind"`
	Role Role          `json:"role"`
}

// RetentionTrigger is the event count in one project that schedules the
// retention job.
const RetentionTrigger = 1_000_000

type DoctorReport struct {
	Nodes         int `json:"nodes"`
	Edges         int `json:"edges"`
	Events        int `json:"events"`
	OrphanEdges   int `json:"orphanEdges"`
	ForwardRefs   int `json:"forwardRefs"`
	ExpiredLeases int `json:"expiredLeases"`
	StaleSessions int `json:"staleSessions"`
	// MaxProjectEvents is the event count of the largest project, MaxEventsProject its key.
	MaxProjectEvents int    `json:"maxProjectEvents"`
	MaxEventsProject string `json:"maxEventsProject"`
	RetainableEvents int    `json:"retainableEvents"`
}
