package search

import (
	"context"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

type Backend interface {
	Ensure(context.Context) error
	Reset(context.Context) error
	Upsert(context.Context, [][]byte) error
	Delete(context.Context, []string) error
	Search(context.Context, model.SearchQuery) ([]model.SearchHit, error)
	Healthy(context.Context) error
	Count(context.Context) (int64, bool, error)
}
