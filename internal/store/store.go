package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > 3 {
		return fmt.Errorf("state database schema version %d is newer than this ohyeah binary", version)
	}
	if version == 3 {
		return nil
	}
	const schema = `
CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    workspace TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    state TEXT NOT NULL DEFAULT 'active',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS data_types (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    command TEXT NOT NULL,
    args_json TEXT NOT NULL DEFAULT '[]',
    revision INTEGER NOT NULL,
    config_hash TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    state TEXT NOT NULL DEFAULT 'active',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(project_id, name)
);
CREATE TABLE IF NOT EXISTS sources (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL DEFAULT '',
    project_workspace TEXT NOT NULL DEFAULT '',
    type_id TEXT NOT NULL DEFAULT '',
    mount_name TEXT NOT NULL DEFAULT '',
    workspace_id TEXT NOT NULL,
    type TEXT NOT NULL,
    path TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    managed_by TEXT NOT NULL DEFAULT 'cli',
    state TEXT NOT NULL DEFAULT 'active',
    type_config_hash TEXT NOT NULL DEFAULT '',
    mount_config_hash TEXT NOT NULL DEFAULT '',
    type_revision INTEGER NOT NULL DEFAULT 0,
    mount_revision INTEGER NOT NULL DEFAULT 0,
    options_json TEXT NOT NULL DEFAULT '{}',
    cursor_json TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS source_objects (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    external_id TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    modified_at TEXT NOT NULL,
    parser_version INTEGER NOT NULL,
    reference_json TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL,
    UNIQUE(source_id, external_id)
);
CREATE TABLE IF NOT EXISTS memory_units (
    id TEXT PRIMARY KEY,
    object_id TEXT NOT NULL REFERENCES source_objects(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL DEFAULT '',
    type_id TEXT NOT NULL DEFAULT '',
    mount_id TEXT NOT NULL DEFAULT '',
    workspace_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    source_type TEXT NOT NULL,
    kind TEXT NOT NULL,
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    thread_id TEXT NOT NULL DEFAULT '',
    turn_id TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    heading TEXT NOT NULL DEFAULT '',
    occurred_at TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    status TEXT NOT NULL,
    requirement_ids_json TEXT NOT NULL DEFAULT '[]',
    repos_json TEXT NOT NULL DEFAULT '[]',
    entities_json TEXT NOT NULL DEFAULT '[]',
    metadata_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_memory_units_source ON memory_units(source_id);
CREATE INDEX IF NOT EXISTS idx_memory_units_thread ON memory_units(thread_id);
CREATE INDEX IF NOT EXISTS idx_memory_units_workspace ON memory_units(workspace_id);
CREATE TABLE IF NOT EXISTS relations (
    id TEXT PRIMARY KEY,
    from_id TEXT NOT NULL,
    to_id TEXT NOT NULL,
    type TEXT NOT NULL,
    confidence REAL NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_relations_to ON relations(to_id, type);
CREATE TABLE IF NOT EXISTS sync_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id TEXT NOT NULL,
    status TEXT NOT NULL,
    scanned INTEGER NOT NULL DEFAULT 0,
    changed INTEGER NOT NULL DEFAULT 0,
    deleted INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    finished_at TEXT
);
CREATE TABLE IF NOT EXISTS index_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    operation TEXT NOT NULL,
    unit_id TEXT NOT NULL,
    payload BLOB,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    delivered_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON index_outbox(delivered_at, id);
CREATE TABLE IF NOT EXISTS schema_versions (
    component TEXT PRIMARY KEY,
    version INTEGER NOT NULL,
    updated_at TEXT NOT NULL
);
INSERT INTO schema_versions(component, version, updated_at)
VALUES ('store', 3, CURRENT_TIMESTAMP)
ON CONFLICT(component) DO UPDATE SET version=excluded.version, updated_at=excluded.updated_at;
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "sources", "cursor_json", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.migrateSourceConfigColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memory_units", "project_id", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memory_units", "type_id", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memory_units", "mount_id", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `PRAGMA user_version=3`)
	return err
}

func (s *Store) migrateSourceConfigColumns(ctx context.Context) error {
	columns := []struct {
		name       string
		definition string
	}{
		{"project_id", `TEXT NOT NULL DEFAULT ''`},
		{"project_workspace", `TEXT NOT NULL DEFAULT ''`},
		{"type_id", `TEXT NOT NULL DEFAULT ''`},
		{"mount_name", `TEXT NOT NULL DEFAULT ''`},
		{"managed_by", `TEXT NOT NULL DEFAULT 'cli'`},
		{"state", `TEXT NOT NULL DEFAULT 'active'`},
		{"type_config_hash", `TEXT NOT NULL DEFAULT ''`},
		{"mount_config_hash", `TEXT NOT NULL DEFAULT ''`},
		{"type_revision", `INTEGER NOT NULL DEFAULT 0`},
		{"mount_revision", `INTEGER NOT NULL DEFAULT 0`},
	}
	for _, column := range columns {
		if err := s.ensureColumn(ctx, "sources", column.name, column.definition); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertSource(ctx context.Context, source model.Source) error {
	return upsertSource(ctx, s.db, source)
}

func upsertSource(ctx context.Context, executor contextExecer, source model.Source) error {
	if source.ID == "" || source.WorkspaceID == "" || source.Driver == "" || source.Path == "" {
		return errors.New("source id, workspace, driver, and path are required")
	}
	now := time.Now().UTC()
	if source.ManagedBy == "" {
		source.ManagedBy = model.ManagedByCLI
	}
	if source.State == "" {
		if source.Enabled {
			source.State = model.SourceStateActive
		} else {
			source.State = model.SourceStateDisabled
		}
	}
	options, _ := json.Marshal(source.Options)
	cursor := string(source.Cursor)
	_, err := executor.ExecContext(ctx, `
INSERT INTO sources(id, project_id, project_workspace, type_id, mount_name, workspace_id, type, path, enabled, managed_by, state, type_config_hash, mount_config_hash, type_revision, mount_revision, options_json, cursor_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  project_id=excluded.project_id, project_workspace=excluded.project_workspace,
  type_id=excluded.type_id, mount_name=excluded.mount_name,
  workspace_id=excluded.workspace_id, type=excluded.type, path=excluded.path,
  enabled=excluded.enabled, managed_by=excluded.managed_by, state=excluded.state,
  type_config_hash=excluded.type_config_hash, mount_config_hash=excluded.mount_config_hash,
  type_revision=excluded.type_revision, mount_revision=excluded.mount_revision,
  options_json=excluded.options_json, updated_at=excluded.updated_at`,
		source.ID, source.ProjectID, source.ProjectWorkspace, source.TypeID, source.MountName, source.WorkspaceID, source.Driver, source.Path,
		boolInt(source.Enabled), source.ManagedBy, source.State, source.TypeConfigHash, source.MountConfigHash,
		source.TypeRevision, source.MountRevision,
		options, cursor, formatTime(now), formatTime(now))
	return err
}

func (s *Store) UpsertProject(ctx context.Context, project model.Project) error {
	return upsertProject(ctx, s.db, project)
}

func upsertProject(ctx context.Context, executor contextExecer, project model.Project) error {
	now := time.Now().UTC()
	if project.State == "" {
		if project.Enabled {
			project.State = model.SourceStateActive
		} else {
			project.State = model.SourceStateDisabled
		}
	}
	_, err := executor.ExecContext(ctx, `
INSERT INTO projects(id, workspace, enabled, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET workspace=excluded.workspace, enabled=excluded.enabled, state=excluded.state, updated_at=excluded.updated_at`,
		project.ID, project.Workspace, boolInt(project.Enabled), project.State, formatTime(now), formatTime(now))
	return err
}

func (s *Store) ListProjects(ctx context.Context) ([]model.Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, workspace, enabled, state, created_at, updated_at FROM projects ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.Project
	for rows.Next() {
		var project model.Project
		var enabled int
		var created, updated string
		if err := rows.Scan(&project.ID, &project.Workspace, &enabled, &project.State, &created, &updated); err != nil {
			return nil, err
		}
		project.Enabled = enabled != 0
		project.CreatedAt = parseTime(created)
		project.UpdatedAt = parseTime(updated)
		result = append(result, project)
	}
	return result, rows.Err()
}

func (s *Store) UpsertDataType(ctx context.Context, dataType model.DataType) error {
	return upsertDataType(ctx, s.db, dataType)
}

func upsertDataType(ctx context.Context, executor contextExecer, dataType model.DataType) error {
	now := time.Now().UTC()
	if dataType.State == "" {
		if dataType.Enabled {
			dataType.State = model.SourceStateActive
		} else {
			dataType.State = model.SourceStateDisabled
		}
	}
	args, _ := json.Marshal(dataType.Args)
	_, err := executor.ExecContext(ctx, `
INSERT INTO data_types(id, project_id, name, command, args_json, revision, config_hash, enabled, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET project_id=excluded.project_id, name=excluded.name, command=excluded.command,
 args_json=excluded.args_json, revision=excluded.revision, config_hash=excluded.config_hash,
 enabled=excluded.enabled, state=excluded.state, updated_at=excluded.updated_at`,
		dataType.ID, dataType.ProjectID, dataType.Name, dataType.Command, args, dataType.Revision,
		dataType.ConfigHash, boolInt(dataType.Enabled), dataType.State, formatTime(now), formatTime(now))
	return err
}

func (s *Store) ListDataTypes(ctx context.Context) ([]model.DataType, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, project_id, name, command, args_json, revision, config_hash, enabled, state, created_at, updated_at FROM data_types ORDER BY project_id, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.DataType
	for rows.Next() {
		var dataType model.DataType
		var args, created, updated string
		var enabled int
		if err := rows.Scan(&dataType.ID, &dataType.ProjectID, &dataType.Name, &dataType.Command, &args,
			&dataType.Revision, &dataType.ConfigHash, &enabled, &dataType.State, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(args), &dataType.Args)
		dataType.Enabled = enabled != 0
		dataType.CreatedAt = parseTime(created)
		dataType.UpdatedAt = parseTime(updated)
		result = append(result, dataType)
	}
	return result, rows.Err()
}

func (s *Store) SetProjectState(ctx context.Context, id, state string, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET state=?, enabled=?, updated_at=? WHERE id=?`, state, boolInt(enabled), formatTime(time.Now().UTC()), id)
	return err
}

func (s *Store) SetDataTypeState(ctx context.Context, id, state string, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE data_types SET state=?, enabled=?, updated_at=? WHERE id=?`, state, boolInt(enabled), formatTime(time.Now().UTC()), id)
	return err
}

func (s *Store) ApplyConfigState(ctx context.Context, change ConfigStateChange) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, project := range change.Projects {
			if err := upsertProject(ctx, tx, project); err != nil {
				return err
			}
		}
		for _, dataType := range change.DataTypes {
			if err := upsertDataType(ctx, tx, dataType); err != nil {
				return err
			}
		}
		for _, mount := range change.Mounts {
			if err := upsertSource(ctx, tx, mount); err != nil {
				return err
			}
		}
		for _, id := range change.DetachProjects {
			if _, err := tx.ExecContext(ctx, `UPDATE projects SET state=?, enabled=0, updated_at=? WHERE id=?`, model.SourceStateDetached, formatTime(time.Now().UTC()), id); err != nil {
				return err
			}
		}
		for _, id := range change.DetachTypes {
			if _, err := tx.ExecContext(ctx, `UPDATE data_types SET state=?, enabled=0, updated_at=? WHERE id=?`, model.SourceStateDetached, formatTime(time.Now().UTC()), id); err != nil {
				return err
			}
		}
		for _, id := range change.DetachMounts {
			if _, err := tx.ExecContext(ctx, `UPDATE sources SET state=?, enabled=0, updated_at=? WHERE id=?`, model.SourceStateDetached, formatTime(time.Now().UTC()), id); err != nil {
				return err
			}
		}
		for _, id := range change.ResetCursors {
			if _, err := tx.ExecContext(ctx, `UPDATE sources SET cursor_json='', updated_at=? WHERE id=?`, formatTime(time.Now().UTC()), id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) RemoveSource(ctx context.Context, id string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM memory_units WHERE source_id=?`, id)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var unitID string
			if err := rows.Scan(&unitID); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, unitID)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, unitID := range ids {
			if err := enqueue(tx, "delete", unitID, nil); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM relations WHERE from_id IN (SELECT value FROM json_each(?)) OR to_id IN (SELECT value FROM json_each(?))`, jsonStringArray(ids), jsonStringArray(ids)); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM sources WHERE id=?`, id)
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

func (s *Store) ListSources(ctx context.Context, enabledOnly bool) ([]model.Source, error) {
	query := `SELECT id, project_id, project_workspace, type_id, mount_name, workspace_id, type, path, enabled, managed_by, state, type_config_hash, mount_config_hash, type_revision, mount_revision, options_json, cursor_json, created_at, updated_at FROM sources`
	if enabledOnly {
		query += ` WHERE enabled=1`
	}
	query += ` ORDER BY workspace_id, id`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.Source
	for rows.Next() {
		var source model.Source
		var enabled int
		var options, cursor, created, updated string
		if err := rows.Scan(&source.ID, &source.ProjectID, &source.ProjectWorkspace, &source.TypeID, &source.MountName, &source.WorkspaceID, &source.Driver, &source.Path,
			&enabled, &source.ManagedBy, &source.State, &source.TypeConfigHash, &source.MountConfigHash,
			&source.TypeRevision, &source.MountRevision,
			&options, &cursor, &created, &updated); err != nil {
			return nil, err
		}
		source.Enabled = enabled != 0
		_ = json.Unmarshal([]byte(options), &source.Options)
		if cursor != "" {
			source.Cursor = json.RawMessage(cursor)
		}
		source.CreatedAt = parseTime(created)
		source.UpdatedAt = parseTime(updated)
		result = append(result, source)
	}
	return result, rows.Err()
}

func (s *Store) UpdateSourceCursor(ctx context.Context, id string, cursor json.RawMessage) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sources SET cursor_json=?, updated_at=? WHERE id=?`, string(cursor), formatTime(time.Now().UTC()), id)
	return err
}

func (s *Store) ResetSourceCursor(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sources SET cursor_json='', updated_at=? WHERE id=?`, formatTime(time.Now().UTC()), id)
	return err
}

func (s *Store) SetSourceState(ctx context.Context, id, state string, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sources SET state=?, enabled=?, updated_at=? WHERE id=?`, state, boolInt(enabled), formatTime(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GetSource(ctx context.Context, id string) (model.Source, error) {
	rows, err := s.ListSources(ctx, false)
	if err != nil {
		return model.Source{}, err
	}
	for _, source := range rows {
		if source.ID == id {
			return source, nil
		}
	}
	return model.Source{}, sql.ErrNoRows
}

func (s *Store) ObjectHash(ctx context.Context, sourceID, externalID string) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT content_hash FROM source_objects WHERE source_id=? AND external_id=?`, sourceID, externalID).Scan(&hash)
	return hash, err
}

func (s *Store) SourceObjectHashes(ctx context.Context, sourceID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT external_id, content_hash FROM source_objects WHERE source_id=?`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var externalID, hash string
		if err := rows.Scan(&externalID, &hash); err != nil {
			return nil, err
		}
		result[externalID] = hash
	}
	return result, rows.Err()
}

func (s *Store) ReplaceDocument(ctx context.Context, doc model.Document) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		return replaceDocumentTx(ctx, tx, doc)
	})
}

func (s *Store) ApplyScanResult(ctx context.Context, sourceID string, result model.ScanResult) (changed, deleted int, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		seen := make(map[string]struct{}, len(result.Documents))
		for _, document := range result.Documents {
			seen[document.Object.ExternalID] = struct{}{}
			var currentHash string
			hashErr := tx.QueryRowContext(ctx, `SELECT content_hash FROM source_objects WHERE source_id=? AND external_id=?`, sourceID, document.Object.ExternalID).Scan(&currentHash)
			if hashErr == nil && currentHash == document.Object.ContentHash {
				continue
			}
			if hashErr != nil && !errors.Is(hashErr, sql.ErrNoRows) {
				return hashErr
			}
			if err := replaceDocumentTx(ctx, tx, document); err != nil {
				return err
			}
			changed++
		}

		var objects []objectRef
		if result.PruneMissing {
			rows, err := tx.QueryContext(ctx, `SELECT id, external_id FROM source_objects WHERE source_id=?`, sourceID)
			if err != nil {
				return err
			}
			for rows.Next() {
				var object objectRef
				if err := rows.Scan(&object.id, &object.externalID); err != nil {
					rows.Close()
					return err
				}
				if _, present := seen[object.externalID]; !present {
					objects = append(objects, object)
				}
			}
			if err := rows.Close(); err != nil {
				return err
			}
		} else if len(result.DeletedExternalIDs) > 0 {
			placeholders := strings.TrimSuffix(strings.Repeat("?,", len(result.DeletedExternalIDs)), ",")
			args := make([]any, 0, len(result.DeletedExternalIDs)+1)
			args = append(args, sourceID)
			for _, externalID := range result.DeletedExternalIDs {
				args = append(args, externalID)
			}
			rows, err := tx.QueryContext(ctx, `SELECT id, external_id FROM source_objects WHERE source_id=? AND external_id IN (`+placeholders+`)`, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var object objectRef
				if err := rows.Scan(&object.id, &object.externalID); err != nil {
					rows.Close()
					return err
				}
				objects = append(objects, object)
			}
			if err := rows.Close(); err != nil {
				return err
			}
		}
		if err := deleteObjectRefsTx(ctx, tx, objects); err != nil {
			return err
		}
		deleted = len(objects)
		if len(result.Cursor) > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE sources SET cursor_json=?, updated_at=? WHERE id=?`, string(result.Cursor), formatTime(time.Now().UTC()), sourceID); err != nil {
				return err
			}
		}
		return nil
	})
	return changed, deleted, err
}

func replaceDocumentTx(ctx context.Context, tx *sql.Tx, doc model.Document) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM memory_units WHERE object_id=?`, doc.Object.ID)
	if err != nil {
		return err
	}
	var oldIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		oldIDs = append(oldIDs, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	ref, _ := json.Marshal(doc.Object.Reference)
	_, err = tx.ExecContext(ctx, `
INSERT INTO source_objects(id, source_id, external_id, content_hash, modified_at, parser_version, reference_json, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET content_hash=excluded.content_hash, modified_at=excluded.modified_at,
 parser_version=excluded.parser_version, reference_json=excluded.reference_json, updated_at=excluded.updated_at`,
		doc.Object.ID, doc.Object.SourceID, doc.Object.ExternalID, doc.Object.ContentHash,
		formatTime(doc.Object.ModifiedAt), doc.Object.ParserVersion, ref, formatTime(time.Now().UTC()))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_units WHERE object_id=?`, doc.Object.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relations WHERE from_id IN (SELECT value FROM json_each(?)) OR to_id IN (SELECT value FROM json_each(?))`, jsonStringArray(oldIDs), jsonStringArray(oldIDs)); err != nil {
		return err
	}
	newIDs := make(map[string]struct{}, len(doc.Units))
	for _, unit := range doc.Units {
		unit.ObjectID = doc.Object.ID
		if err := insertUnit(ctx, tx, unit); err != nil {
			return err
		}
		payload, err := json.Marshal(unit)
		if err != nil {
			return err
		}
		if err := enqueue(tx, "upsert", unit.ID, payload); err != nil {
			return err
		}
		newIDs[unit.ID] = struct{}{}
	}
	for _, oldID := range oldIDs {
		if _, retained := newIDs[oldID]; !retained {
			if err := enqueue(tx, "delete", oldID, nil); err != nil {
				return err
			}
		}
	}
	for _, relation := range doc.Relations {
		_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO relations(id, from_id, to_id, type, confidence, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			relation.ID, relation.FromID, relation.ToID, relation.Type, relation.Confidence, formatTime(relation.CreatedAt))
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteMissingObjects(ctx context.Context, sourceID string, seen map[string]struct{}) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, external_id FROM source_objects WHERE source_id=?`, sourceID)
	if err != nil {
		return 0, err
	}
	var missing []objectRef
	for rows.Next() {
		var candidate objectRef
		if err := rows.Scan(&candidate.id, &candidate.externalID); err != nil {
			rows.Close()
			return 0, err
		}
		if _, ok := seen[candidate.externalID]; !ok {
			missing = append(missing, candidate)
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	err = s.deleteObjectRefs(ctx, missing)
	return len(missing), err
}

func (s *Store) DeleteObjects(ctx context.Context, sourceID string, externalIDs []string) (int, error) {
	if len(externalIDs) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(externalIDs)), ",")
	args := make([]any, 0, len(externalIDs)+1)
	args = append(args, sourceID)
	for _, externalID := range externalIDs {
		args = append(args, externalID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, external_id FROM source_objects WHERE source_id=? AND external_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return 0, err
	}
	var objects []objectRef
	for rows.Next() {
		var object objectRef
		if err := rows.Scan(&object.id, &object.externalID); err != nil {
			rows.Close()
			return 0, err
		}
		objects = append(objects, object)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	return len(objects), s.deleteObjectRefs(ctx, objects)
}

type objectRef struct{ id, externalID string }

func (s *Store) deleteObjectRefs(ctx context.Context, objects []objectRef) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		return deleteObjectRefsTx(ctx, tx, objects)
	})
}

func deleteObjectRefsTx(ctx context.Context, tx *sql.Tx, objects []objectRef) error {
	for _, object := range objects {
		unitRows, err := tx.QueryContext(ctx, `SELECT id FROM memory_units WHERE object_id=?`, object.id)
		if err != nil {
			return err
		}
		var unitIDs []string
		for unitRows.Next() {
			var id string
			if err := unitRows.Scan(&id); err != nil {
				unitRows.Close()
				return err
			}
			unitIDs = append(unitIDs, id)
		}
		if err := unitRows.Close(); err != nil {
			return err
		}
		for _, id := range unitIDs {
			if err := enqueue(tx, "delete", id, nil); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM relations WHERE from_id IN (SELECT value FROM json_each(?)) OR to_id IN (SELECT value FROM json_each(?))`, jsonStringArray(unitIDs), jsonStringArray(unitIDs)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM source_objects WHERE id=?`, object.id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetUnit(ctx context.Context, id string) (model.MemoryUnit, error) {
	row := s.db.QueryRowContext(ctx, unitSelect+` WHERE id=?`, id)
	return scanUnit(row)
}

func (s *Store) GetMemory(ctx context.Context, id string) (model.MemoryRecord, error) {
	unit, err := s.GetUnit(ctx, id)
	if err != nil {
		return model.MemoryRecord{}, err
	}
	references, err := s.ObjectReferences(ctx, []string{unit.ObjectID})
	if err != nil {
		return model.MemoryRecord{}, err
	}
	return model.MemoryRecord{Unit: unit, Reference: references[unit.ObjectID]}, nil
}

func (s *Store) ObjectReferences(ctx context.Context, objectIDs []string) (map[string]map[string]string, error) {
	result := make(map[string]map[string]string, len(objectIDs))
	if len(objectIDs) == 0 {
		return result, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(objectIDs)), ",")
	args := make([]any, len(objectIDs))
	for index, id := range objectIDs {
		args[index] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, reference_json FROM source_objects WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, encoded string
		if err := rows.Scan(&id, &encoded); err != nil {
			return nil, err
		}
		var reference map[string]string
		if err := json.Unmarshal([]byte(encoded), &reference); err != nil {
			return nil, err
		}
		result[id] = reference
	}
	return result, rows.Err()
}

func (s *Store) CorrectingUnits(ctx context.Context, ids []string) (map[string][]string, error) {
	result := make(map[string][]string)
	if len(ids) == 0 {
		return result, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT to_id, from_id FROM relations WHERE type IN ('corrects','supersedes') AND to_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var to, from string
		if err := rows.Scan(&to, &from); err != nil {
			return nil, err
		}
		result[to] = append(result[to], from)
	}
	return result, rows.Err()
}

func (s *Store) StartSyncRun(ctx context.Context, sourceID string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO sync_runs(source_id, status, started_at) VALUES (?, 'running', ?)`, sourceID, formatTime(time.Now().UTC()))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) FinishSyncRun(ctx context.Context, run model.SyncRun) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sync_runs SET status=?, scanned=?, changed=?, deleted=?, error=?, finished_at=? WHERE id=?`,
		run.Status, run.Scanned, run.Changed, run.Deleted, run.Error, formatTime(time.Now().UTC()), run.ID)
	return err
}

func (s *Store) ListSyncRuns(ctx context.Context, sourceID string, limit int) ([]model.SyncRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `SELECT id, source_id, status, scanned, changed, deleted, error, started_at, finished_at FROM sync_runs`
	args := []any{}
	if sourceID != "" {
		query += ` WHERE source_id=?`
		args = append(args, sourceID)
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
			if _, err := tx.ExecContext(ctx, `UPDATE index_outbox SET delivered_at=? WHERE id=?`, formatTime(time.Now().UTC()), id); err != nil {
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

func (s *Store) MemoryPayloadBatch(ctx context.Context, afterID string, limit int) (payloads [][]byte, nextID string, err error) {
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
		payload, err := json.Marshal(unit)
		if err != nil {
			return nil, "", err
		}
		payloads = append(payloads, payload)
		nextID = unit.ID
	}
	return payloads, nextID, rows.Err()
}

func (s *Store) MemoryCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_units`).Scan(&count)
	return count, err
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

func (s *Store) ensureColumn(ctx context.Context, table, column, definition string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+definition)
	return err
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
