package syncer

import (
	"context"
	"errors"

	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/internal/search"
)

type VectorSyncResult struct {
	Updated   int64 `json:"updated"`
	Unchanged int64 `json:"unchanged"`
	Deleted   int64 `json:"deleted"`
}

func (s *Service) SyncVectorIndex(ctx context.Context, rebuild, dryRun bool) (VectorSyncResult, error) {
	backend, ok := s.backend.(search.VectorBackend)
	if !ok {
		return VectorSyncResult{}, errors.New("backend does not support vectors")
	}
	if rebuild && dryRun {
		return VectorSyncResult{}, errors.New("vector rebuild cannot be a dry run")
	}
	if rebuild {
		if err := backend.ResetVectors(ctx); err != nil {
			return VectorSyncResult{}, err
		}
	} else if !dryRun {
		if err := backend.EnsureVectors(ctx); err != nil {
			return VectorSyncResult{}, err
		}
	}
	existing, err := backend.VectorDocuments(ctx)
	if err != nil {
		return VectorSyncResult{}, err
	}
	var afterID string
	var result VectorSyncResult
	for {
		payloads, nextID, err := s.store.MemoryBatch(ctx, afterID, 500)
		if err != nil {
			return result, err
		}
		if len(payloads) == 0 {
			break
		}
		changed := make([]model.MemoryUnit, 0, len(payloads))
		for _, payload := range payloads {
			unit := payload
			previous, found := existing[unit.ID]
			delete(existing, unit.ID)
			unit.Content = ""
			if found && model.EqualIndexMetadata(previous, unit) {
				result.Unchanged++
				continue
			}
			changed = append(changed, payload)
		}
		if !dryRun {
			for offset := 0; offset < len(changed); offset += 64 {
				end := min(offset+64, len(changed))
				if err := backend.UpsertVectors(ctx, changed[offset:end]); err != nil {
					return result, err
				}
			}
		}
		result.Updated += int64(len(changed))
		afterID = nextID
	}
	ids := make([]string, 0, 500)
	for id := range existing {
		ids = append(ids, id)
		if len(ids) == 500 {
			if !dryRun {
				if err := backend.DeleteVectors(ctx, ids); err != nil {
					return result, err
				}
			}
			result.Deleted += int64(len(ids))
			ids = ids[:0]
		}
	}
	if !dryRun {
		if err := backend.DeleteVectors(ctx, ids); err != nil {
			return result, err
		}
	}
	result.Deleted += int64(len(ids))
	return result, nil
}
