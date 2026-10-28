package syncer

import (
	"context"
	"errors"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

func (s *Service) Search(ctx context.Context, query model.SearchQuery) ([]model.SearchHit, error) {
	if query.Limit < 0 || query.Limit > 200 {
		return nil, errors.New("search limit must be between 0 and 200")
	}
	if query.Limit == 0 {
		query.Limit = 20
	}
	hits, err := s.backend.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(hits))
	objectIDs := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.Unit.ID)
		objectIDs = append(objectIDs, hit.Unit.ObjectID)
	}
	references, err := s.store.ObjectReferences(ctx, objectIDs)
	if err != nil {
		return nil, err
	}
	corrections, err := s.store.CorrectingUnits(ctx, ids)
	if err != nil {
		return nil, err
	}
	liveUnits, err := s.store.UnitsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := make([]model.SearchHit, 0, query.Limit)
	seenObjects := make(map[string]struct{})
	for _, hit := range hits {
		live, found := liveUnits[hit.Unit.ID]
		if !found || live.Status == model.StatusRetracted || live.Status == model.StatusSuperseded {
			continue
		}
		live.Content = hit.Unit.Content // Preserve the search adapter's cropped excerpt.
		if !model.EqualIndexMetadata(live, hit.Unit) {
			continue
		}
		hit.Reference = references[hit.Unit.ObjectID]
		if correcting := corrections[hit.Unit.ID]; len(correcting) > 0 {
			hit.Superseded = true
			hit.CorrectedBy = correcting
			continue
		}
		if _, duplicate := seenObjects[hit.Unit.ObjectID]; duplicate {
			continue
		}
		seenObjects[hit.Unit.ObjectID] = struct{}{}
		result = append(result, hit)
		if query.Limit > 0 && len(result) >= query.Limit {
			break
		}
	}
	return result, nil
}
