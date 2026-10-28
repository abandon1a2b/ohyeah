package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

type Retention struct {
	Outbox   time.Duration
	SyncRuns time.Duration
	Audit    time.Duration
}

type PruneResult struct {
	Outbox   int64 `json:"outbox"`
	SyncRuns int64 `json:"syncRuns"`
	Audit    int64 `json:"audit"`
	DryRun   bool  `json:"dryRun"`
}

// Prune never removes pending work, incomplete audits, or idempotency receipts.
// Zero retention means retain forever. Counts and deletions share a transaction.
func (s *Store) Prune(ctx context.Context, policy Retention, dryRun bool) (result PruneResult, err error) {
	result.DryRun = dryRun
	if policy.Outbox < 0 || policy.SyncRuns < 0 || policy.Audit < 0 {
		return result, errors.New("retention must not be negative")
	}
	now := time.Now().UTC()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		for _, rule := range []struct {
			table, where string
			cutoff       any
			duration     time.Duration
			count        *int64
		}{
			{"index_outbox", "delivered_at IS NOT NULL AND delivered_at < ?", formatTime(now.Add(-policy.Outbox)), policy.Outbox, &result.Outbox},
			{"sync_runs", "status != 'running' AND finished_at IS NOT NULL AND finished_at < ?", formatTime(now.Add(-policy.SyncRuns)), policy.SyncRuns, &result.SyncRuns},
			{"audit_events", "status != 0 AND occurred_at < ?", now.Add(-policy.Audit).Unix(), policy.Audit, &result.Audit},
		} {
			if rule.duration == 0 {
				continue
			}
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+rule.table+" WHERE "+rule.where, rule.cutoff).Scan(rule.count); err != nil {
				return err
			}
			if !dryRun {
				if _, err := tx.ExecContext(ctx, "DELETE FROM "+rule.table+" WHERE "+rule.where, rule.cutoff); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return
}

// Backup writes a transactionally consistent standalone SQLite database. The
// caller must choose a new destination; existing files are never overwritten.
func (s *Store) Backup(ctx context.Context, destination string) error {
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			_ = os.Remove(destination)
		}
	}()
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", destination); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	backup, err := sql.Open("sqlite", destination)
	if err != nil {
		return err
	}
	defer backup.Close()
	var integrity string
	if err := backup.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("backup integrity: %s", integrity)
	}
	completed = true
	return nil
}

func (s *Store) Check(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return err
	}
	for rows.Next() {
		var message string
		if err := rows.Scan(&message); err != nil {
			rows.Close()
			return err
		}
		if message != "ok" {
			rows.Close()
			return errors.New(message)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("foreign key violation")
	}
	return rows.Err()
}
