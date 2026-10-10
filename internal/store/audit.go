package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type AuditEvent struct {
	ID           int64           `json:"id"`
	OccurredAt   int64           `json:"occurredAt"`
	Issuer       string          `json:"issuer"`
	Subject      string          `json:"subject"`
	CredentialID string          `json:"credentialId"`
	Action       string          `json:"action"`
	Resource     string          `json:"resource"`
	Status       int             `json:"status"`
	Details      json.RawMessage `json:"details"`
}

func (s *Store) StartAudit(ctx context.Context, e AuditEvent) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO audit_events(occurred_at,issuer,subject,credential_id,action,resource,status,details) VALUES(?,?,?,?,?,?,0,'{}')`, time.Now().Unix(), e.Issuer, e.Subject, e.CredentialID, e.Action, e.Resource)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) FinishAudit(ctx context.Context, id int64, status int, resource string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE audit_events SET status=?,resource=? WHERE id=? AND status=0`, status, resource, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (s *Store) ListAudit(ctx context.Context, issuer, subject string, all bool, before int64, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,occurred_at,issuer,subject,credential_id,action,resource,status,details FROM audit_events WHERE issuer=? AND (subject=? OR ?) AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?`, issuer, subject, all, before, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AuditEvent{}
	for rows.Next() {
		var e AuditEvent
		var details string
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.Issuer, &e.Subject, &e.CredentialID, &e.Action, &e.Resource, &e.Status, &details); err != nil {
			return nil, err
		}
		e.Details = json.RawMessage(details)
		result = append(result, e)
	}
	return result, rows.Err()
}
