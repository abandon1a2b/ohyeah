package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) migrateAuth(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS auth_credentials (
 id TEXT PRIMARY KEY, token_hash TEXT UNIQUE NOT NULL, subject TEXT NOT NULL, issuer TEXT NOT NULL,
 kind TEXT NOT NULL, name TEXT NOT NULL, expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS auth_flows (
 key TEXT PRIMARY KEY, kind TEXT NOT NULL, payload TEXT NOT NULL, subject TEXT NOT NULL DEFAULT '', expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_auth_flows_expiry ON auth_flows(expires_at);
UPDATE schema_versions SET version=5, updated_at=CURRENT_TIMESTAMP WHERE component='store';
PRAGMA user_version=5;`)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.migrateCentral(ctx)
}

type Credential struct {
	ID        string `json:"id"`
	Subject   string `json:"subject"`
	Issuer    string `json:"issuer"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	ExpiresAt int64  `json:"expiresAt"`
	CreatedAt int64  `json:"createdAt"`
}

func (s *Store) CreateCredential(ctx context.Context, c Credential, hash string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO auth_credentials(id,token_hash,subject,issuer,kind,name,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?)`, c.ID, hash, c.Subject, c.Issuer, c.Kind, c.Name, c.ExpiresAt, c.CreatedAt)
	return err
}

func (s *Store) FindCredential(ctx context.Context, hash, kind string) (Credential, error) {
	var c Credential
	err := s.db.QueryRowContext(ctx, `SELECT id,subject,issuer,kind,name,expires_at,created_at FROM auth_credentials WHERE token_hash=? AND kind=? AND expires_at>?`, hash, kind, time.Now().Unix()).Scan(&c.ID, &c.Subject, &c.Issuer, &c.Kind, &c.Name, &c.ExpiresAt, &c.CreatedAt)
	return c, err
}

func (s *Store) ListCredentials(ctx context.Context, subject, issuer string) ([]Credential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,subject,issuer,kind,name,expires_at,created_at FROM auth_credentials WHERE subject=? AND issuer=? AND expires_at>? ORDER BY created_at DESC`, subject, issuer, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Credential{}
	for rows.Next() {
		var c Credential
		if err := rows.Scan(&c.ID, &c.Subject, &c.Issuer, &c.Kind, &c.Name, &c.ExpiresAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *Store) RevokeCredential(ctx context.Context, id, subject, issuer string, admin bool) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM auth_credentials WHERE id=? AND ((subject=? AND issuer=?) OR ?)`, id, subject, issuer, admin)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
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
