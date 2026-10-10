package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCredentialPersistenceAndRevocation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c := Credential{ID: "one", Issuer: "issuer", Subject: "alice", Kind: "token", ExpiresAt: time.Now().Add(time.Hour).Unix(), CreatedAt: time.Now().Unix()}
	if err := s.CreateCredential(ctx, c, "hash-only"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.FindCredential(ctx, "hash-only", "token"); err != nil || got.Subject != "alice" || got.Issuer != "issuer" {
		t.Fatalf("restart: %#v %v", got, err)
	}
	if _, err := s.FindCredential(ctx, "hash-only", "session"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("token accepted as session")
	}
	if err := s.RevokeCredential(ctx, "one", "bob", "issuer", false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cross-account revocation accepted")
	}
	if err := s.RevokeCredential(ctx, "one", "alice", "different-issuer", false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cross-issuer revocation accepted")
	}
	if err := s.RevokeCredential(ctx, "one", "admin", "issuer", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FindCredential(ctx, "hash-only", "token"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("revoked token accepted")
	}
}

func TestFlowSingleUseAndExpiry(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.PutAuthFlow(ctx, "flow", "cli", "payload"); err != nil {
		t.Fatal(err)
	}
	if err := s.ApproveAuthFlow(ctx, "flow", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := s.ApproveAuthFlow(ctx, "flow", "bob"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("flow approved twice")
	}
	var group sync.WaitGroup
	var consumed atomic.Int32
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, subject, err := s.AuthFlow(ctx, "flow", "cli", true)
			if err == nil {
				consumed.Add(1)
				if subject != "alice" {
					t.Errorf("wrong subject %s", subject)
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if consumed.Load() != 1 {
		t.Fatalf("flow consumed %d times", consumed.Load())
	}
	if err := s.PutAuthFlow(ctx, "expired", "oidc", "payload"); err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`UPDATE auth_flows SET expires_at=0 WHERE key='expired'`)
	if _, _, err := s.AuthFlow(ctx, "expired", "oidc", true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired flow accepted")
	}
}
