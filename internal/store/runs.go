package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

func (s *Store) StartSyncRun(ctx context.Context, sourceID string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO sync_runs(source_id, status, started_at) VALUES (?, 'running', ?)`, sourceID, formatTime(time.Now().UTC()))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) FinishSyncRun(ctx context.Context, run model.SyncRun) error {
	if run.FinishedAt == nil {
		return errors.New("sync run finish time is required")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE sync_runs SET status=?, scanned=?, changed=?, deleted=?, error=?, finished_at=? WHERE id=?`,
		run.Status, run.Scanned, run.Changed, run.Deleted, run.Error, formatTime(*run.FinishedAt), run.ID)
	return err
}

func (s *Store) ListSyncRuns(ctx context.Context, sourceID string, limit int) ([]model.SyncRun, error) {
	var sources []string
	if sourceID != "" {
		sources = []string{sourceID}
	}
	return s.ListSyncRunsForSources(ctx, sources, limit)
}

// nil selects all sources for administrators; an empty slice selects none.
func (s *Store) ListSyncRunsForSources(ctx context.Context, sources []string, limit int) ([]model.SyncRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `SELECT id, source_id, status, scanned, changed, deleted, error, started_at, finished_at FROM sync_runs`
	args := []any{}
	if sources != nil {
		encoded, err := json.Marshal(sources)
		if err != nil {
			return nil, err
		}
		query += ` WHERE source_id IN (SELECT value FROM json_each(?))`
		args = append(args, string(encoded))
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []model.SyncRun
	for rows.Next() {
		var run model.SyncRun
		var started string
		var finished sql.NullString
		if err := rows.Scan(&run.ID, &run.SourceID, &run.Status, &run.Scanned, &run.Changed, &run.Deleted, &run.Error, &started, &finished); err != nil {
			return nil, err
		}
		run.StartedAt = parseTime(started)
		if finished.Valid {
			value := parseTime(finished.String)
			run.FinishedAt = &value
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}
