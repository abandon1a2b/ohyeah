package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

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

var ErrSourceChanged = errors.New("source changed during collection; retry using its current configuration")

func (s *Store) ApplyScanResult(ctx context.Context, sourceID string, result model.ScanResult, audit ...AuditEvent) (changed, deleted int, err error) {
	return s.commitScan(ctx, sourceID, result, nil, audit...)
}

// CommitCollection rejects results collected against a superseded configuration.
func (s *Store) CommitCollection(ctx context.Context, expected model.Source, result model.ScanResult, audit ...AuditEvent) (changed, deleted int, err error) {
	return s.commitScan(ctx, expected.ID, result, &expected, audit...)
}

func (s *Store) commitScan(ctx context.Context, sourceID string, result model.ScanResult, expected *model.Source, audit ...AuditEvent) (changed, deleted int, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if expected != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE sources SET id=id WHERE id=?`, sourceID); err != nil {
				return err
			}
			var current model.Source
			var execution, options string
			err := tx.QueryRowContext(ctx, `SELECT type,enabled,state,type_config_hash,mount_config_hash,path,project_workspace,type_revision,mount_revision,writer,execution_json,options_json,project_id,type_id,workspace_id FROM sources WHERE id=?`, sourceID).Scan(&current.Driver, &current.Enabled, &current.State, &current.TypeConfigHash, &current.MountConfigHash, &current.Path, &current.ProjectWorkspace, &current.TypeRevision, &current.MountRevision, &current.Writer, &execution, &options, &current.ProjectID, &current.TypeID, &current.WorkspaceID)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSourceChanged
			}
			if err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(execution), &current.Execution); err != nil {
				return err
			}
			expectedOptions, err := json.Marshal(expected.Options)
			if err != nil {
				return err
			}
			var actualOptions, normalizedExpected any
			if err := json.Unmarshal([]byte(options), &actualOptions); err != nil {
				return err
			}
			if err := json.Unmarshal(expectedOptions, &normalizedExpected); err != nil {
				return err
			}
			sameExecution := current.Execution.Command == expected.Execution.Command && current.Execution.Timeout == expected.Execution.Timeout && slices.Equal(current.Execution.Args, expected.Execution.Args)
			if !reflect.DeepEqual(actualOptions, normalizedExpected) || current.ProjectID != expected.ProjectID || current.TypeID != expected.TypeID || current.WorkspaceID != expected.WorkspaceID || !current.Enabled || current.State != model.SourceStateActive || UploadScope(current) != UploadScope(*expected) || !sameExecution {
				return ErrSourceChanged
			}
		}
		changed, deleted, err = applyScanResultTx(ctx, tx, sourceID, result)
		if err != nil {
			return err
		}
		if len(audit) > 0 {
			e := audit[0]
			details, err := json.Marshal(map[string]int{"changed": changed, "deleted": deleted})
			if err != nil {
				return err
			}
			kind := e.ActorKind
			if kind == "" {
				kind = "principal"
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(occurred_at,issuer,subject,credential_id,action,resource,status,details,actor_kind) VALUES(?,?,?,?,?,?,200,?,?)`, time.Now().Unix(), e.Issuer, e.Subject, e.CredentialID, e.Action, sourceID, string(details), kind)
			return err
		}
		return nil
	})
	return
}

func applyScanResultTx(ctx context.Context, tx *sql.Tx, sourceID string, result model.ScanResult) (changed, deleted int, err error) {
	err = func() error {
		seen := make(map[string]struct{}, len(result.Documents))
		for _, document := range result.Documents {
			if document.Object.SourceID != sourceID {
				return errors.New("scan document belongs to a different source")
			}
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
		// Reconcile declared relations after all document changes, including
		// unchanged owners whose targets were deleted and later recreated.
		for _, document := range result.Documents {
			if err := replaceRelationsTx(ctx, tx, document); err != nil {
				return err
			}
		}
		deleted = len(objects)
		if len(result.Cursor) > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE sources SET cursor_json=?, updated_at=? WHERE id=?`, string(result.Cursor), formatTime(time.Now().UTC()), sourceID); err != nil {
				return err
			}
		}
		return nil
	}()
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM relations WHERE owner_object_id=?`, doc.Object.ID); err != nil {
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
			if _, err := tx.ExecContext(ctx, `DELETE FROM relations WHERE from_id=? OR to_id=?`, oldID, oldID); err != nil {
				return err
			}
			if err := enqueue(tx, "delete", oldID, nil); err != nil {
				return err
			}
		}
	}
	return replaceRelationsTx(ctx, tx, doc)
}

func replaceRelationsTx(ctx context.Context, tx *sql.Tx, doc model.Document) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM relations WHERE owner_object_id=?`, doc.Object.ID); err != nil {
		return err
	}
	for _, relation := range doc.Relations {
		_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO relations(id, owner_object_id, from_id, to_id, type, confidence, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			relation.ID, doc.Object.ID, relation.FromID, relation.ToID, relation.Type, relation.Confidence, formatTime(relation.CreatedAt))
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

func (s *Store) UnitsByID(ctx context.Context, ids []string) (map[string]model.MemoryUnit, error) {
	result := make(map[string]model.MemoryUnit, len(ids))
	data, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, unitSelect+` WHERE id IN (SELECT value FROM json_each(?))`, string(data))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		unit, err := scanUnit(rows)
		if err != nil {
			return nil, err
		}
		result[unit.ID] = unit
	}
	return result, rows.Err()
}
