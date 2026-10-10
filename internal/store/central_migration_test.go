package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

func TestUpgradeVersionFivePreservesDataAndCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	registered := model.Source{ID: "source", WorkspaceID: "workspace", Driver: model.DriverCommand, Path: "/documents", Enabled: true}
	if err := s.UpsertSource(ctx, registered); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSourceCursor(ctx, "source", []byte(`{"offset":3}`)); err != nil {
		t.Fatal(err)
	}
	c := Credential{ID: "credential", Issuer: "issuer", Subject: "alice", Kind: "token", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err := s.CreateCredential(ctx, c, "hashed-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE upload_batches;DROP TABLE upload_state;DROP TABLE audit_events;DROP TABLE auth_principals;ALTER TABLE auth_credentials DROP COLUMN last_used_at;PRAGMA user_version=5;UPDATE schema_versions SET version=5 WHERE component='store'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetSource(ctx, "source")
	if err != nil || string(got.Cursor) != `{"offset":3}` {
		t.Fatalf("migration lost source: %#v %v", got, err)
	}
	credential, err := s.FindCredential(ctx, "hashed-secret", "token")
	if err != nil || credential.Subject != "alice" {
		t.Fatalf("migration lost credentials: %#v %v", credential, err)
	}
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 8 {
		t.Fatalf("migration version=%d %v", version, err)
	}
}
