package syncer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/internal/search"
	"github.com/abandon1a2b/ohyeah/internal/source"
	"github.com/abandon1a2b/ohyeah/internal/store"
)

type Service struct {
	store      *store.Store
	backend    search.Backend
	config     config.Config
	locks      sync.Map
	dispatchMu sync.Mutex
}

type DoctorResult struct {
	SQLiteCount      int64 `json:"sqliteCount"`
	MeilisearchCount int64 `json:"meilisearchCount"`
	IndexExists      bool  `json:"indexExists"`
	Consistent       bool  `json:"consistent"`
}

func New(store *store.Store, backend search.Backend, cfg config.Config) *Service {
	return &Service{store: store, backend: backend, config: cfg}
}

func (s *Service) SyncAll(ctx context.Context) ([]model.SyncRun, error) {
	sources, err := s.store.ListSources(ctx, true)
	if err != nil {
		return nil, err
	}
	runs := make([]model.SyncRun, 0, len(sources))
	var syncErrors []error
	for _, registered := range sources {
		run, syncErr := s.SyncSource(ctx, registered)
		runs = append(runs, run)
		if syncErr != nil {
			syncErrors = append(syncErrors, fmt.Errorf("%s: %w", registered.ID, syncErr))
		}
	}
	if err := s.Dispatch(ctx); err != nil {
		syncErrors = append(syncErrors, fmt.Errorf("dispatch: %w", err))
	}
	return runs, errors.Join(syncErrors...)
}

func (s *Service) SyncByID(ctx context.Context, id string) (model.SyncRun, error) {
	registered, err := s.store.GetSource(ctx, id)
	if err != nil {
		return model.SyncRun{}, err
	}
	run, err := s.SyncSource(ctx, registered)
	if err != nil {
		return run, err
	}
	return run, s.Dispatch(ctx)
}

func (s *Service) SyncSource(ctx context.Context, registered model.Source) (run model.SyncRun, resultErr error) {
	lockValue, _ := s.locks.LoadOrStore(registered.ID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	runID, err := s.store.StartSyncRun(ctx, registered.ID)
	if err != nil {
		return run, err
	}
	run = model.SyncRun{ID: runID, SourceID: registered.ID, Status: "running", StartedAt: time.Now().UTC()}
	defer func() {
		if resultErr != nil {
			run.Status = "failed"
			run.Error = resultErr.Error()
		} else {
			run.Status = "succeeded"
		}
		finished := time.Now().UTC()
		run.FinishedAt = &finished
		if finishErr := s.store.FinishSyncRun(context.Background(), run); finishErr != nil && resultErr == nil {
			resultErr = finishErr
		}
	}()

	scanResult, err := s.collect(ctx, registered)
	if err != nil {
		return run, err
	}
	run.Scanned = len(scanResult.Documents)
	changed, deleted, err := s.store.ApplyScanResult(ctx, registered.ID, scanResult)
	if err != nil {
		return run, err
	}
	run.Changed, run.Deleted = changed, deleted
	return run, nil
}

func (s *Service) PreviewByID(ctx context.Context, id string) (model.SyncPreview, error) {
	registered, err := s.store.GetSource(ctx, id)
	if err != nil {
		return model.SyncPreview{}, err
	}
	return s.PreviewSource(ctx, registered)
}

func (s *Service) PreviewAll(ctx context.Context) ([]model.SyncPreview, error) {
	sources, err := s.store.ListSources(ctx, true)
	if err != nil {
		return nil, err
	}
	previews := make([]model.SyncPreview, 0, len(sources))
	var previewErrors []error
	for _, registered := range sources {
		preview, err := s.PreviewSource(ctx, registered)
		if err != nil {
			preview.Error = err.Error()
			previewErrors = append(previewErrors, fmt.Errorf("%s: %w", registered.ID, err))
		}
		previews = append(previews, preview)
	}
	return previews, errors.Join(previewErrors...)
}

func (s *Service) PreviewSource(ctx context.Context, registered model.Source) (model.SyncPreview, error) {
	preview := model.SyncPreview{SourceID: registered.ID}
	result, err := s.collect(ctx, registered)
	if err != nil {
		return preview, err
	}
	existing, err := s.store.SourceObjectHashes(ctx, registered.ID)
	if err != nil {
		return preview, err
	}
	seen := make(map[string]struct{}, len(result.Documents))
	for _, document := range result.Documents {
		preview.Documents++
		preview.MemoryUnits += len(document.Units)
		seen[document.Object.ExternalID] = struct{}{}
		if existing[document.Object.ExternalID] != document.Object.ContentHash {
			preview.WouldChange++
		}
	}
	if result.PruneMissing {
		for externalID := range existing {
			if _, present := seen[externalID]; !present {
				preview.WouldDelete++
			}
		}
	} else {
		for _, externalID := range result.DeletedExternalIDs {
			if _, present := existing[externalID]; present {
				preview.WouldDelete++
			}
		}
	}
	preview.PruneMissing = result.PruneMissing
	preview.CursorReturned = len(result.Cursor) > 0
	return preview, nil
}

func (s *Service) collect(ctx context.Context, registered model.Source) (model.ScanResult, error) {
	scanner, err := source.New(registered)
	if err != nil {
		return model.ScanResult{}, err
	}
	timeout := collectorTimeout(registered, s.config.Sync.CollectorTimeout)
	collectorCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := scanner.Scan(collectorCtx, registered, s.config.Sync.MaxFileSize)
	if err != nil && errors.Is(collectorCtx.Err(), context.DeadlineExceeded) {
		return model.ScanResult{}, fmt.Errorf("collector timed out after %s", timeout)
	}
	return result, err
}

func collectorTimeout(source model.Source, fallback time.Duration) time.Duration {
	value, _ := source.Options["__ohyeah_timeout"].(string)
	if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

func (s *Service) Dispatch(ctx context.Context) error {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if err := s.backend.Ensure(ctx); err != nil {
		return err
	}
	for {
		items, err := s.store.PendingOutbox(ctx, s.config.Meilisearch.BatchSize)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		var upserts [][]byte
		var upsertRows, deleteRows []int64
		var deletes []string
		for _, item := range items {
			switch item.Operation {
			case "upsert":
				upserts = append(upserts, item.Payload)
				upsertRows = append(upsertRows, item.ID)
			case "delete":
				deletes = append(deletes, item.UnitID)
				deleteRows = append(deleteRows, item.ID)
			default:
				return fmt.Errorf("unknown outbox operation %q", item.Operation)
			}
		}
		if err := s.backend.Upsert(ctx, upserts); err != nil {
			_ = s.store.MarkOutboxFailed(ctx, upsertRows, err)
			return err
		}
		if err := s.store.MarkOutboxDelivered(ctx, upsertRows); err != nil {
			return err
		}
		if err := s.backend.Delete(ctx, deletes); err != nil {
			_ = s.store.MarkOutboxFailed(ctx, deleteRows, err)
			return err
		}
		if err := s.store.MarkOutboxDelivered(ctx, deleteRows); err != nil {
			return err
		}
	}
}

func (s *Service) Search(ctx context.Context, query model.SearchQuery) ([]model.SearchHit, error) {
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
	result := make([]model.SearchHit, 0, query.Limit)
	seenObjects := make(map[string]struct{})
	for _, hit := range hits {
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

func (s *Service) RebuildIndex(ctx context.Context) (int64, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if err := s.backend.Reset(ctx); err != nil {
		return 0, err
	}
	const batchSize = 500
	var afterID string
	var indexed int64
	for {
		payloads, nextID, err := s.store.MemoryPayloadBatch(ctx, afterID, batchSize)
		if err != nil {
			return indexed, err
		}
		if len(payloads) == 0 {
			break
		}
		if err := s.backend.Upsert(ctx, payloads); err != nil {
			return indexed, err
		}
		indexed += int64(len(payloads))
		afterID = nextID
	}
	result, err := s.Doctor(ctx)
	if err != nil {
		return indexed, err
	}
	if !result.Consistent {
		return indexed, fmt.Errorf("index rebuild count mismatch: sqlite=%d meilisearch=%d", result.SQLiteCount, result.MeilisearchCount)
	}
	return indexed, nil
}

func (s *Service) Doctor(ctx context.Context) (DoctorResult, error) {
	sqliteCount, err := s.store.MemoryCount(ctx)
	if err != nil {
		return DoctorResult{}, err
	}
	meiliCount, exists, err := s.backend.Count(ctx)
	if err != nil {
		return DoctorResult{}, err
	}
	return DoctorResult{SQLiteCount: sqliteCount, MeilisearchCount: meiliCount, IndexExists: exists, Consistent: exists && sqliteCount == meiliCount}, nil
}

func (s *Service) RunPeriodic(ctx context.Context) error {
	ticker := time.NewTicker(s.config.Sync.ReconcileInterval)
	defer ticker.Stop()
	if _, err := s.SyncAll(ctx); err != nil {
		slog.Warn("initial sync failed", "error", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := s.SyncAll(ctx); err != nil {
				slog.Warn("scheduled sync failed", "error", err)
			}
		}
	}
}
