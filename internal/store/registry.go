package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

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
	execution, err := json.Marshal(source.Execution)
	if err != nil {
		return err
	}
	cursor := string(source.Cursor)
	_, err = executor.ExecContext(ctx, `
INSERT INTO sources(id, project_id, project_workspace, type_id, mount_name, workspace_id, type, path, enabled, managed_by, state, type_config_hash, mount_config_hash, type_revision, mount_revision, options_json, execution_json, writer, cursor_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  project_id=excluded.project_id, project_workspace=excluded.project_workspace,
  type_id=excluded.type_id, mount_name=excluded.mount_name,
  workspace_id=excluded.workspace_id, type=excluded.type, path=excluded.path,
  enabled=excluded.enabled, managed_by=excluded.managed_by, state=excluded.state,
  type_config_hash=excluded.type_config_hash, mount_config_hash=excluded.mount_config_hash,
  type_revision=excluded.type_revision, mount_revision=excluded.mount_revision,
  options_json=excluded.options_json, execution_json=excluded.execution_json, writer=excluded.writer, updated_at=excluded.updated_at`,
		source.ID, source.ProjectID, source.ProjectWorkspace, source.TypeID, source.MountName, source.WorkspaceID, source.Driver, source.Path,
		boolInt(source.Enabled), source.ManagedBy, source.State, source.TypeConfigHash, source.MountConfigHash,
		source.TypeRevision, source.MountRevision,
		options, execution, source.Writer, cursor, formatTime(now), formatTime(now))
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
	where := ""
	if enabledOnly {
		where = "enabled=1"
	}
	return s.listSources(ctx, where)
}

func (s *Store) listSources(ctx context.Context, where string, args ...any) ([]model.Source, error) {
	query := `SELECT id, project_id, project_workspace, type_id, mount_name, workspace_id, type, path, enabled, managed_by, state, type_config_hash, mount_config_hash, type_revision, mount_revision, options_json, execution_json, writer, cursor_json, created_at, updated_at FROM sources`
	if where != "" {
		query += " WHERE " + where
	}
	query += ` ORDER BY workspace_id, id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.Source
	for rows.Next() {
		var source model.Source
		var enabled int
		var options, execution, cursor, created, updated string
		if err := rows.Scan(&source.ID, &source.ProjectID, &source.ProjectWorkspace, &source.TypeID, &source.MountName, &source.WorkspaceID, &source.Driver, &source.Path,
			&enabled, &source.ManagedBy, &source.State, &source.TypeConfigHash, &source.MountConfigHash,
			&source.TypeRevision, &source.MountRevision,
			&options, &execution, &source.Writer, &cursor, &created, &updated); err != nil {
			return nil, err
		}
		source.Enabled = enabled != 0
		if err := json.Unmarshal([]byte(options), &source.Options); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(execution), &source.Execution); err != nil {
			return nil, err
		}
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
	rows, err := s.listSources(ctx, "id=?", id)
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
