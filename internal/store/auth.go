package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Credential struct {
	ID         string `json:"id"`
	Subject    string `json:"subject"`
	Issuer     string `json:"issuer"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	ExpiresAt  int64  `json:"expiresAt"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt int64  `json:"lastUsedAt"`
}

func (s *Store) CreateCredential(ctx context.Context, c Credential, hash string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO auth_credentials(id,token_hash,subject,issuer,kind,name,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?)`, c.ID, hash, c.Subject, c.Issuer, c.Kind, c.Name, c.ExpiresAt, c.CreatedAt); err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]string{"kind": c.Kind})
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_events(occurred_at,issuer,subject,credential_id,action,resource,status,details) VALUES(?,?,?,?,?,?,200,?)`, time.Now().Unix(), c.Issuer, c.Subject, c.ID, "credential.issue", c.ID, string(details))
		return err
	})
}

func (s *Store) FindCredential(ctx context.Context, hash, kind string) (Credential, error) {
	var c Credential
	err := s.db.QueryRowContext(ctx, `SELECT id,subject,issuer,kind,name,expires_at,created_at,last_used_at FROM auth_credentials WHERE token_hash=? AND kind=? AND expires_at>?`, hash, kind, time.Now().Unix()).Scan(&c.ID, &c.Subject, &c.Issuer, &c.Kind, &c.Name, &c.ExpiresAt, &c.CreatedAt, &c.LastUsedAt)
	return c, err
}

func (s *Store) ListCredentials(ctx context.Context, subject, issuer string, all bool) ([]Credential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,subject,issuer,kind,name,expires_at,created_at,last_used_at FROM auth_credentials WHERE (subject=? OR ?) AND issuer=? AND expires_at>? ORDER BY created_at DESC`, subject, all, issuer, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Credential{}
	for rows.Next() {
		var c Credential
		if err := rows.Scan(&c.ID, &c.Subject, &c.Issuer, &c.Kind, &c.Name, &c.ExpiresAt, &c.CreatedAt, &c.LastUsedAt); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *Store) TouchCredential(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE auth_credentials SET last_used_at=? WHERE id=? AND last_used_at<?`, time.Now().Unix(), id, time.Now().Unix()-60)
	return err
}

func (s *Store) RevokeCredential(ctx context.Context, id, subject, issuer string, admin bool) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM auth_credentials WHERE id=? AND ((subject=? AND issuer=?) OR ?)`, id, subject, issuer, admin)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return sql.ErrNoRows
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(occurred_at,issuer,subject,credential_id,action,resource,status,details) VALUES(?,?,?,?,?,?,200,'{}')`, time.Now().Unix(), issuer, subject, id, "credential.revoke", id)
		return err
	})
}

func (s *Store) PutAuthFlow(ctx context.Context, key, kind, payload string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_flows WHERE expires_at<=?`, time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_credentials WHERE expires_at<=?`, time.Now().Unix()); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM auth_flows`).Scan(&count); err != nil {
		return err
	}
	if count >= 1000 {
		return ErrAuthBusy
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_flows(key,kind,payload,expires_at) VALUES(?,?,?,?)`, key, kind, payload, time.Now().Add(10*time.Minute).Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

var ErrAuthBusy = errors.New("too many pending login requests")

func (s *Store) AuthFlow(ctx context.Context, key, kind string, consume bool) (string, string, error) {
	query := `SELECT payload,subject FROM auth_flows WHERE key=? AND kind=? AND expires_at>?`
	if consume {
		query = `DELETE FROM auth_flows WHERE key=? AND kind=? AND expires_at>? RETURNING payload,subject`
	}
	var payload, subject string
	err := s.db.QueryRowContext(ctx, query, key, kind, time.Now().Unix()).Scan(&payload, &subject)
	return payload, subject, err
}

func (s *Store) ApproveAuthFlow(ctx context.Context, key, subject string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE auth_flows SET subject=? WHERE key=? AND kind='cli' AND subject='' AND expires_at>?`, subject, key, time.Now().Unix())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
