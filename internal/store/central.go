package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/identity"
	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/pkg/collector"
)

func (s *Store) migrateCentral(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS upload_state(source_id TEXT PRIMARY KEY REFERENCES sources(id) ON DELETE CASCADE, version INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS upload_batches(source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE, batch_id TEXT NOT NULL, request_hash TEXT NOT NULL, receipt_json TEXT NOT NULL, PRIMARY KEY(source_id,batch_id));
CREATE TABLE IF NOT EXISTS audit_events(id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at INTEGER NOT NULL, issuer TEXT NOT NULL, subject TEXT NOT NULL, credential_id TEXT NOT NULL, action TEXT NOT NULL, resource TEXT NOT NULL, status INTEGER NOT NULL, details TEXT NOT NULL DEFAULT '{}');
CREATE INDEX IF NOT EXISTS idx_audit_subject ON audit_events(issuer,subject,id DESC);
CREATE TABLE IF NOT EXISTS auth_principals(issuer TEXT NOT NULL, subject TEXT NOT NULL, policy_json TEXT NOT NULL, version INTEGER NOT NULL, PRIMARY KEY(issuer,subject));
UPDATE schema_versions SET version=6, updated_at=CURRENT_TIMESTAMP WHERE component='store';
PRAGMA user_version=6;`)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.migrateIdentity(ctx)
}

var ErrUploadConflict = errors.New("upload version, scope, or batch ID conflict; prepare a new batch from current source state")
var ErrUploadSource = errors.New("source is not an active upload source owned by this writer")

func UploadScope(source model.Source) string {
	writer, _ := source.Options["__ohyeah_writer"].(string)
	return identity.Hash(source.TypeConfigHash, source.MountConfigHash, source.Driver, source.Path, source.ProjectWorkspace, writer, strconv.Itoa(source.TypeRevision), strconv.Itoa(source.MountRevision))
}

type UploadState struct {
	SourceID      string          `json:"sourceId"`
	Version       int64           `json:"version"`
	ScopeRevision string          `json:"scopeRevision"`
	Cursor        json.RawMessage `json:"cursor"`
}

func (s *Store) UploadState(ctx context.Context, source model.Source) (UploadState, error) {
	result := UploadState{SourceID: source.ID}
	var current model.Source
	var options, cursor string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(u.version,0),s.cursor_json,s.type_config_hash,s.mount_config_hash,s.type,s.path,s.project_workspace,s.options_json,s.type_revision,s.mount_revision FROM sources s LEFT JOIN upload_state u ON u.source_id=s.id WHERE s.id=?`, source.ID).Scan(&result.Version, &cursor, &current.TypeConfigHash, &current.MountConfigHash, &current.Driver, &current.Path, &current.ProjectWorkspace, &options, &current.TypeRevision, &current.MountRevision)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(options), &current.Options); err != nil {
		return result, err
	}
	result.ScopeRevision = UploadScope(current)
	if cursor != "" {
		result.Cursor = json.RawMessage(cursor)
	}
	if result.ScopeRevision != UploadScope(source) {
		return result, ErrUploadConflict
	}
	return result, nil
}

func (s *Store) CommitUpload(ctx context.Context, subject, issuer, credential string, batch collector.UploadBatch, requestHash string, result model.ScanResult) (receipt collector.UploadReceipt, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		// Take the write lock before reading versions; competing batches then serialize.
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO upload_state(source_id) SELECT id FROM sources WHERE id=?`, batch.SourceID); err != nil {
			return err
		}
		var current model.Source
		var options string
		if err := tx.QueryRowContext(ctx, `SELECT type,enabled,state,options_json,type_config_hash,mount_config_hash,path,project_workspace,type_revision,mount_revision FROM sources WHERE id=?`, batch.SourceID).Scan(&current.Driver, &current.Enabled, &current.State, &options, &current.TypeConfigHash, &current.MountConfigHash, &current.Path, &current.ProjectWorkspace, &current.TypeRevision, &current.MountRevision); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(options), &current.Options); err != nil {
			return err
		}
		writer, _ := current.Options["__ohyeah_writer"].(string)
		if current.Driver != model.DriverUpload || !current.Enabled || current.State != model.SourceStateActive || writer != subject {
			return ErrUploadSource
		}
		var previousHash, previousJSON string
		err := tx.QueryRowContext(ctx, `SELECT request_hash,receipt_json FROM upload_batches WHERE source_id=? AND batch_id=?`, batch.SourceID, batch.BatchID).Scan(&previousHash, &previousJSON)
		if err == nil {
			if previousHash != requestHash {
				return ErrUploadConflict
			}
			if err := json.Unmarshal([]byte(previousJSON), &receipt); err != nil {
				return err
			}
			receipt.Replayed = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT version FROM upload_state WHERE source_id=?`, batch.SourceID).Scan(&version); err != nil {
			return err
		}
		if version != batch.ExpectedVersion || batch.ScopeRevision != UploadScope(current) {
			return ErrUploadConflict
		}
		changed, deleted, err := applyScanResultTx(ctx, tx, batch.SourceID, result)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		run, err := tx.ExecContext(ctx, `INSERT INTO sync_runs(source_id,status,scanned,changed,deleted,started_at,finished_at) VALUES(?,'succeeded',?,?,?,?,?)`, batch.SourceID, len(result.Documents), changed, deleted, formatTime(now), formatTime(now))
		if err != nil {
			return err
		}
		runID, err := run.LastInsertId()
		if err != nil {
			return err
		}
		receipt = collector.UploadReceipt{SourceID: batch.SourceID, BatchID: batch.BatchID, Version: version + 1, RunID: runID, Changed: changed, Deleted: deleted}
		data, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO upload_batches VALUES(?,?,?,?)`, batch.SourceID, batch.BatchID, requestHash, string(data)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE upload_state SET version=? WHERE source_id=?`, version+1, batch.SourceID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(occurred_at,issuer,subject,credential_id,action,resource,status,details) VALUES(?,?,?,?,?,?,200,?)`, now.Unix(), issuer, subject, credential, "upload.commit", batch.SourceID, string(data))
		return err
	})
	return
}
