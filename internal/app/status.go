package app

import (
	"context"

	"github.com/abandon1a2b/ohyeah/internal/syncer"
)

// StatusResult omits administrative state for ordinary readers.
type StatusResult struct {
	Backend          string               `json:"backend"`
	EmbeddingEnabled bool                 `json:"embeddingEnabled"`
	Stats            map[string]int64     `json:"stats,omitempty"`
	StatePath        string               `json:"statePath,omitempty"`
	Index            string               `json:"index,omitempty"`
	Doctor           *syncer.DoctorResult `json:"doctor,omitempty"`
	DoctorError      string               `json:"doctorError,omitempty"`
}

func (a *App) Status(ctx context.Context, admin bool) (StatusResult, error) {
	result := StatusResult{Backend: "available", EmbeddingEnabled: a.Config.Meilisearch.Embedding.Enabled}
	if !admin {
		return result, nil
	}
	stats, err := a.Store.Stats(ctx)
	if err != nil {
		return result, err
	}
	result.Stats, result.StatePath, result.Index = stats, a.Config.StatePath, a.Config.Meilisearch.Index
	if err := a.Backend.Healthy(ctx); err != nil {
		result.Backend = err.Error()
	}
	doctor, err := a.Syncer.Doctor(ctx)
	result.Doctor = &doctor
	if err != nil {
		result.DoctorError = err.Error()
	}
	return result, nil
}
