package model

import (
	"encoding/json"
	"time"
)

const (
	DriverCommand       = "command"
	ManagedByCLI        = "cli"
	ManagedByConfig     = "config"
	SourceStateActive   = "active"
	SourceStateDisabled = "disabled"
	SourceStateDetached = "detached"

	KindRequest      = "request"
	KindConclusion   = "conclusion"
	KindCorrection   = "correction"
	KindDocument     = "document"
	KindDecision     = "decision"
	KindChange       = "change"
	KindVerification = "verification"
	KindTodo         = "todo"

	StatusObserved   = "observed"
	StatusConfirmed  = "confirmed"
	StatusSuperseded = "superseded"
	StatusRetracted  = "retracted"

	RelationCorrects    = "corrects"
	RelationSupersedes  = "supersedes"
	RelationSupports    = "supports"
	RelationContinues   = "continues"
	RelationDerivedFrom = "derived_from"
)

type Source struct {
	ID               string          `json:"id"`
	ProjectID        string          `json:"projectId,omitempty"`
	ProjectWorkspace string          `json:"projectWorkspace,omitempty"`
	TypeID           string          `json:"typeId,omitempty"`
	MountName        string          `json:"mountName,omitempty"`
	WorkspaceID      string          `json:"workspaceId"`
	Driver           string          `json:"driver"`
	Path             string          `json:"path"`
	Enabled          bool            `json:"enabled"`
	ManagedBy        string          `json:"managedBy"`
	State            string          `json:"state"`
	TypeConfigHash   string          `json:"typeConfigHash,omitempty"`
	MountConfigHash  string          `json:"mountConfigHash,omitempty"`
	TypeRevision     int             `json:"typeRevision,omitempty"`
	MountRevision    int             `json:"mountRevision,omitempty"`
	Options          map[string]any  `json:"options,omitempty"`
	Cursor           json.RawMessage `json:"cursor,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
}

type Project struct {
	ID        string    `json:"id"`
	Workspace string    `json:"workspace"`
	Enabled   bool      `json:"enabled"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type DataType struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"projectId"`
	Name       string    `json:"name"`
	Command    string    `json:"command"`
	Args       []string  `json:"args,omitempty"`
	Revision   int       `json:"revision"`
	ConfigHash string    `json:"configHash"`
	Enabled    bool      `json:"enabled"`
	State      string    `json:"state"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type SourceObject struct {
	ID            string            `json:"id"`
	SourceID      string            `json:"sourceId"`
	ExternalID    string            `json:"externalId"`
	ContentHash   string            `json:"contentHash"`
	ModifiedAt    time.Time         `json:"modifiedAt"`
	ParserVersion int               `json:"parserVersion"`
	Reference     map[string]string `json:"reference,omitempty"`
}

type MemoryUnit struct {
	ID             string            `json:"id"`
	ObjectID       string            `json:"objectId"`
	ProjectID      string            `json:"projectId,omitempty"`
	TypeID         string            `json:"typeId,omitempty"`
	MountID        string            `json:"mountId,omitempty"`
	WorkspaceID    string            `json:"workspaceId"`
	SourceID       string            `json:"sourceId"`
	SourceType     string            `json:"sourceType"`
	Kind           string            `json:"kind"`
	Title          string            `json:"title"`
	Content        string            `json:"content"`
	ThreadID       string            `json:"threadId,omitempty"`
	TurnID         string            `json:"turnId,omitempty"`
	Path           string            `json:"path,omitempty"`
	Heading        string            `json:"heading,omitempty"`
	OccurredAt     time.Time         `json:"occurredAt"`
	ContentHash    string            `json:"contentHash"`
	Status         string            `json:"status"`
	RequirementIDs []string          `json:"requirementIds,omitempty"`
	Repos          []string          `json:"repos,omitempty"`
	Entities       []string          `json:"entities,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type Relation struct {
	ID         string    `json:"id"`
	FromID     string    `json:"fromId"`
	ToID       string    `json:"toId"`
	Type       string    `json:"type"`
	Confidence float64   `json:"confidence"`
	CreatedAt  time.Time `json:"createdAt"`
}

type Document struct {
	Object    SourceObject `json:"object"`
	Units     []MemoryUnit `json:"units"`
	Relations []Relation   `json:"relations,omitempty"`
}

type ScanResult struct {
	Documents          []Document      `json:"documents"`
	DeletedExternalIDs []string        `json:"deletedExternalIds,omitempty"`
	Cursor             json.RawMessage `json:"cursor,omitempty"`
	PruneMissing       bool            `json:"pruneMissing"`
}

type SyncRun struct {
	ID         int64      `json:"id"`
	SourceID   string     `json:"sourceId"`
	Status     string     `json:"status"`
	Scanned    int        `json:"scanned"`
	Changed    int        `json:"changed"`
	Deleted    int        `json:"deleted"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

type SyncPreview struct {
	SourceID       string `json:"sourceId"`
	Documents      int    `json:"documents"`
	MemoryUnits    int    `json:"memoryUnits"`
	WouldChange    int    `json:"wouldChange"`
	WouldDelete    int    `json:"wouldDelete"`
	PruneMissing   bool   `json:"pruneMissing"`
	CursorReturned bool   `json:"cursorReturned"`
	Error          string `json:"error,omitempty"`
}

type SearchQuery struct {
	Query       string
	ProjectID   string
	TypeID      string
	MountID     string
	WorkspaceID string
	Kinds       []string
	Limit       int
}

type SearchHit struct {
	Unit        MemoryUnit        `json:"unit"`
	Reference   map[string]string `json:"reference,omitempty"`
	Score       float64           `json:"score,omitempty"`
	Superseded  bool              `json:"superseded"`
	CorrectedBy []string          `json:"correctedBy,omitempty"`
}

type MemoryRecord struct {
	Unit      MemoryUnit        `json:"unit"`
	Reference map[string]string `json:"reference,omitempty"`
}
