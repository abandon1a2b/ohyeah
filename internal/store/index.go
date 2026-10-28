package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

func (s *Store) PendingOutbox(ctx context.Context, limit int) ([]OutboxItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, operation, unit_id, payload, attempts FROM index_outbox WHERE delivered_at IS NULL ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []OutboxItem
	for rows.Next() {
		var item OutboxItem
		if err := rows.Scan(&item.ID, &item.Operation, &item.UnitID, &item.Payload, &item.Attempts); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) MarkOutboxDelivered(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE index_outbox SET delivered_at=?, payload=NULL WHERE id=?`, formatTime(time.Now().UTC()), id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) MarkOutboxFailed(ctx context.Context, ids []int64, cause error) error {
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, `UPDATE index_outbox SET attempts=attempts+1, last_error=? WHERE id=?`, cause.Error(), id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Stats(ctx context.Context) (map[string]int64, error) {
	queries := map[string]string{
		"sources":       `SELECT COUNT(*) FROM sources`,
		"objects":       `SELECT COUNT(*) FROM source_objects`,
		"memoryUnits":   `SELECT COUNT(*) FROM memory_units`,
		"relations":     `SELECT COUNT(*) FROM relations`,
		"pendingOutbox": `SELECT COUNT(*) FROM index_outbox WHERE delivered_at IS NULL`,
		"failedOutbox":  `SELECT COUNT(*) FROM index_outbox WHERE delivered_at IS NULL AND attempts > 0`,
	}
	result := make(map[string]int64, len(queries))
	for name, query := range queries {
		var value int64
		if err := s.db.QueryRowContext(ctx, query).Scan(&value); err != nil {
			return nil, err
		}
		result[name] = value
	}
	return result, nil
}

func (s *Store) MemoryBatch(ctx context.Context, afterID string, limit int) (units []model.MemoryUnit, nextID string, err error) {
	rows, err := s.db.QueryContext(ctx, unitSelect+` WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	for rows.Next() {
		unit, err := scanUnit(rows)
		if err != nil {
			return nil, "", err
		}
		units = append(units, unit)
		nextID = unit.ID
	}
	return units, nextID, rows.Err()
}

func (s *Store) MemoryCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_units`).Scan(&count)
	return count, err
}
