package collector

import (
	"encoding/json"
	"time"
)

const ProtocolVersion = 1

const (
	EnvelopeDocument   = "document"
	EnvelopeDelete     = "delete"
	EnvelopeDiagnostic = "diagnostic"
	EnvelopeComplete   = "complete"
)

type Request struct {
	ProtocolVersion int             `json:"protocolVersion"`
	Project         Project         `json:"project"`
	Type            DataType        `json:"type"`
	Mount           Mount           `json:"mount"`
	Cursor          json.RawMessage `json:"cursor,omitempty"`
	MaxFileSize     int64           `json:"maxFileSize"`
}

type Project struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
}

type DataType struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
}

type Mount struct {
	ID       string         `json:"id"`
	Root     string         `json:"root"`
	Revision int            `json:"revision"`
	Options  map[string]any `json:"options,omitempty"`
}

type Envelope struct {
	Type       string          `json:"type"`
	Document   *Document       `json:"document,omitempty"`
	ExternalID string          `json:"externalId,omitempty"`
	Cursor     json.RawMessage `json:"cursor,omitempty"`
	Mode       string          `json:"mode,omitempty"`
	Level      string          `json:"level,omitempty"`
	Message    string          `json:"message,omitempty"`
}

type Document struct {
	ExternalID string            `json:"externalId"`
	SourceType string            `json:"sourceType,omitempty"`
	ModifiedAt time.Time         `json:"modifiedAt,omitempty"`
	Reference  map[string]string `json:"reference,omitempty"`
	Units      []Unit            `json:"units"`
	Relations  []Relation        `json:"relations,omitempty"`
}

type Unit struct {
	Key            string            `json:"key"`
	Kind           string            `json:"kind"`
	Title          string            `json:"title"`
	Content        string            `json:"content"`
	ThreadID       string            `json:"threadId,omitempty"`
	TurnID         string            `json:"turnId,omitempty"`
	Path           string            `json:"path,omitempty"`
	Heading        string            `json:"heading,omitempty"`
	OccurredAt     time.Time         `json:"occurredAt,omitempty"`
	Status         string            `json:"status,omitempty"`
	RequirementIDs []string          `json:"requirementIds,omitempty"`
	Repos          []string          `json:"repos,omitempty"`
	Entities       []string          `json:"entities,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type Relation struct {
	FromExternalID string  `json:"fromExternalId,omitempty"`
	FromKey        string  `json:"fromKey"`
	ToExternalID   string  `json:"toExternalId,omitempty"`
	ToKey          string  `json:"toKey"`
	Type           string  `json:"type"`
	Confidence     float64 `json:"confidence,omitempty"`
}
