package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/config"
)

type PrincipalPolicy struct {
	Subject    string                  `json:"subject"`
	Version    int64                   `json:"version"`
	Principal  *config.PrincipalConfig `json:"principal"`
	Configured bool                    `json:"configured"`
}

var ErrPolicyConflict = errors.New("permissions changed concurrently; reload before saving")

func (s *Store) PrincipalPolicy(ctx context.Context, issuer, subject string) (PrincipalPolicy, error) {
	result := PrincipalPolicy{Subject: subject}
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT policy_json,version FROM auth_principals WHERE issuer=? AND subject=?`, issuer, subject).Scan(&data, &result.Version)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal([]byte(data), &result.Principal)
	return result, err
}

func (s *Store) ListPrincipalPolicies(ctx context.Context, issuer string) ([]PrincipalPolicy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT subject,policy_json,version FROM auth_principals WHERE issuer=? ORDER BY subject`, issuer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PrincipalPolicy{}
	for rows.Next() {
		var p PrincipalPolicy
		var data string
		if err := rows.Scan(&p.Subject, &data, &p.Version); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &p.Principal); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *Store) SavePrincipalPolicy(ctx context.Context, issuer, subject string, expected int64, p *config.PrincipalConfig) (PrincipalPolicy, error) {
	var result PrincipalPolicy
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO auth_principals(issuer,subject,policy_json,version) VALUES(?,?,'null',0)`, issuer, subject); err != nil {
			return err
		}
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT version FROM auth_principals WHERE issuer=? AND subject=?`, issuer, subject).Scan(&version); err != nil {
			return err
		}
		if version != expected {
			return ErrPolicyConflict
		}
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE auth_principals SET policy_json=?,version=version+1 WHERE issuer=? AND subject=?`, string(data), issuer, subject); err != nil {
			return err
		}
		result = PrincipalPolicy{Subject: subject, Version: version + 1, Principal: p}
		return nil
	})
	return result, err
}

func (s *Store) RotateCredential(ctx context.Context, id, subject, issuer string, admin bool, replacement Credential, hash string) (Credential, error) {
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var owner, name string
		var expiry int64
		if err := tx.QueryRowContext(ctx, `DELETE FROM auth_credentials WHERE id=? AND issuer=? AND kind='token' AND expires_at>? AND (subject=? OR ?) RETURNING subject,name,expires_at`, id, issuer, time.Now().Unix(), subject, admin).Scan(&owner, &name, &expiry); err != nil {
			return err
		}
		replacement.Subject = owner
		replacement.Name = name
		replacement.Issuer = issuer
		replacement.Kind = "token"
		replacement.ExpiresAt = expiry
		_, err := tx.ExecContext(ctx, `INSERT INTO auth_credentials(id,token_hash,subject,issuer,kind,name,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?)`, replacement.ID, hash, owner, issuer, "token", name, expiry, replacement.CreatedAt)
		if err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]string{"replacementId": replacement.ID})
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(occurred_at,issuer,subject,credential_id,action,resource,status,details) VALUES(?,?,?,?,?,?,200,?)`, time.Now().Unix(), issuer, subject, id, "credential.rotate", id, string(details))
		return err
	})
	return replacement, err
}
