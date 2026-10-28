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

var ErrUploadConflict = errors.New("upload version, scope, or batch ID conflict; prepare a new batch from current source state")
var ErrUploadSource = errors.New("source is not an active upload source owned by this writer")

func UploadScope(source model.Source) string {
	writer := source.Writer
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
	var cursor string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(u.version,0),s.cursor_json,s.type_config_hash,s.mount_config_hash,s.type,s.path,s.project_workspace,s.writer,s.type_revision,s.mount_revision FROM sources s LEFT JOIN upload_state u ON u.source_id=s.id WHERE s.id=?`, source.ID).Scan(&result.Version, &cursor, &current.TypeConfigHash, &current.MountConfigHash, &current.Driver, &current.Path, &current.ProjectWorkspace, &current.Writer, &current.TypeRevision, &current.MountRevision)
	if err != nil {
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
		if err := tx.QueryRowContext(ctx, `SELECT type,enabled,state,writer,type_config_hash,mount_config_hash,path,project_workspace,type_revision,mount_revision FROM sources WHERE id=?`, batch.SourceID).Scan(&current.Driver, &current.Enabled, &current.State, &current.Writer, &current.TypeConfigHash, &current.MountConfigHash, &current.Path, &current.ProjectWorkspace, &current.TypeRevision, &current.MountRevision); err != nil {
			return err
		}
		writer := current.Writer
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
