package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/config"
)

func TestPolicyPersistenceAndResetVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p := config.PrincipalConfig{Subject: "alice", Projects: []string{"one"}}
	if _, err := s.SavePrincipalPolicy(ctx, "issuer", "alice", 0, &p); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	policy, err := s.PrincipalPolicy(ctx, "issuer", "alice")
	if err != nil || policy.Version != 1 || policy.Principal.Projects[0] != "one" {
		t.Fatalf("persisted policy: %#v %v", policy, err)
	}
	if _, err := s.PrincipalPolicy(ctx, "different", "alice"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("issuer policy leak")
	}
	if _, err := s.SavePrincipalPolicy(ctx, "issuer", "alice", 1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePrincipalPolicy(ctx, "issuer", "alice", 0, &p); !errors.Is(err, ErrPolicyConflict) {
		t.Fatal("reset lost concurrency version")
	}
}

func TestFailedRotationPreservesOldCredential(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	c := Credential{ID: "old", Subject: "alice", Issuer: "issuer", Kind: "token", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err := s.CreateCredential(ctx, c, "old-hash"); err != nil {
		t.Fatal(err)
	}
	c.ID = "occupied"
	if err := s.CreateCredential(ctx, c, "another-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RotateCredential(ctx, "old", "alice", "issuer", false, Credential{ID: "occupied"}, "replacement-hash"); err == nil {
		t.Fatal("duplicate replacement ID accepted")
	}
	if _, err := s.FindCredential(ctx, "old-hash", "token"); err != nil {
		t.Fatal("failed rotation destroyed old token")
	}
}
