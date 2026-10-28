package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/model"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type OutboxItem struct {
	ID        int64
	Operation string
	UnitID    string
	Payload   []byte
	Attempts  int
}

type ConfigStateChange struct {
	Projects       []model.Project
	DataTypes      []model.DataType
	Mounts         []model.Source
	DetachProjects []string
	DetachTypes    []string
	DetachMounts   []string
	ResetCursors   []string
}

type contextExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;`); err != nil {
		db.Close()
		return nil, err
	}
	var journalMode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		db.Close()
		return nil, err
	}
	if journalMode != "wal" {
		if err := db.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&journalMode); err != nil {
			db.Close()
			return nil, err
		}
	}
	s := &Store{db: db}
	if err := s.initialize(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

//go:embed schema.sql
var schema string

const schemaVersion = 9

func (s *Store) initialize(ctx context.Context) error {
	var version, tables int
	if err := s.db.QueryRowContext(ctx, `SELECT user_version, (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%') FROM pragma_user_version`).Scan(&version, &tables); err != nil {
		return err
	}
	if version == schemaVersion {
		return nil
	}
	if version != 0 {
		return fmt.Errorf("unsupported state database schema version %d (expected %d); pre-release database migrations are not supported; use a new state_path", version, schemaVersion)
	}
	if tables != 0 {
		return errors.New("unversioned state database is not empty; use a new state_path")
	}

	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, schema)
		return err
	})
}

func insertUnit(ctx context.Context, tx *sql.Tx, unit model.MemoryUnit) error {
	requirements, _ := json.Marshal(unit.RequirementIDs)
	repos, _ := json.Marshal(unit.Repos)
	entities, _ := json.Marshal(unit.Entities)
	metadata, _ := json.Marshal(unit.Metadata)
	_, err := tx.ExecContext(ctx, `
INSERT OR REPLACE INTO memory_units(
 id, object_id, project_id, type_id, mount_id, workspace_id, source_id, source_type, kind, title, content,
 thread_id, turn_id, path, heading, occurred_at, content_hash, status,
 requirement_ids_json, repos_json, entities_json, metadata_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		unit.ID, unit.ObjectID, unit.ProjectID, unit.TypeID, unit.MountID,
		unit.WorkspaceID, unit.SourceID, unit.SourceType, unit.Kind,
		unit.Title, unit.Content, unit.ThreadID, unit.TurnID, unit.Path, unit.Heading,
		formatTime(unit.OccurredAt), unit.ContentHash, unit.Status,
		requirements, repos, entities, metadata)
	return err
}

func enqueue(tx *sql.Tx, operation, unitID string, payload []byte) error {
	_, err := tx.Exec(`INSERT INTO index_outbox(operation, unit_id, payload, created_at) VALUES (?, ?, ?, ?)`, operation, unitID, payload, formatTime(time.Now().UTC()))
	return err
}

const unitSelect = `SELECT id, object_id, project_id, type_id, mount_id, workspace_id, source_id, source_type, kind, title, content,
thread_id, turn_id, path, heading, occurred_at, content_hash, status,
requirement_ids_json, repos_json, entities_json, metadata_json FROM memory_units`

type scanner interface{ Scan(...any) error }

func scanUnit(row scanner) (model.MemoryUnit, error) {
	var unit model.MemoryUnit
	var occurred, requirements, repos, entities, metadata string
	err := row.Scan(&unit.ID, &unit.ObjectID, &unit.ProjectID, &unit.TypeID, &unit.MountID,
		&unit.WorkspaceID, &unit.SourceID, &unit.SourceType,
		&unit.Kind, &unit.Title, &unit.Content, &unit.ThreadID, &unit.TurnID, &unit.Path,
		&unit.Heading, &occurred, &unit.ContentHash, &unit.Status,
		&requirements, &repos, &entities, &metadata)
	if err != nil {
		return model.MemoryUnit{}, err
	}
	unit.OccurredAt = parseTime(occurred)
	_ = json.Unmarshal([]byte(requirements), &unit.RequirementIDs)
	_ = json.Unmarshal([]byte(repos), &unit.Repos)
	_ = json.Unmarshal([]byte(entities), &unit.Entities)
	_ = json.Unmarshal([]byte(metadata), &unit.Metadata)
	return unit, nil
}

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		value = time.Now().UTC()
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func jsonStringArray(values []string) string {
	data, _ := json.Marshal(values)
	return string(data)
}
