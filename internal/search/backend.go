package search

import (
	"context"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

type Backend interface {
	Ensure(context.Context) error
	Reset(context.Context) error
	Upsert(context.Context, []model.MemoryUnit) error
	Delete(context.Context, []string) error
	Search(context.Context, model.SearchQuery) ([]model.SearchHit, error)
	Healthy(context.Context) error
	Count(context.Context) (int64, bool, error)
}

// VectorBackend is an optional capability; full-text indexing does not depend on it.
type VectorBackend interface {
	ResetVectors(context.Context) error
	EnsureVectors(context.Context) error
	VectorDocuments(context.Context) (map[string]model.MemoryUnit, error)
	UpsertVectors(context.Context, []model.MemoryUnit) error
	DeleteVectors(context.Context, []string) error
}

var _ Backend = (*Meilisearch)(nil)
var _ VectorBackend = (*Meilisearch)(nil)
