package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupIsCompleteAndNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateCredential(ctx, Credential{ID: "c", Subject: "alice", Issuer: "issuer", Kind: "token", Name: "test", ExpiresAt: time.Now().Add(time.Hour).Unix()}, "hash"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "backup.db")
	if err := st.Backup(ctx, path); err != nil {
		t.Fatal(err)
	}
	if err := st.Backup(ctx, path); !os.IsExist(err) {
		t.Fatalf("overwriting existing backup: %v", err)
	}
	restored, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	credential, err := restored.FindCredential(ctx, "hash", "token")
	if err != nil || credential.Subject != "alice" {
		t.Fatalf("restored credential=%v error=%v", credential, err)
	}
	if err := restored.Check(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRetentionPreservesPendingWorkAndSecurityHistory(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.db.Exec(`INSERT INTO index_outbox(operation,unit_id,payload,created_at,delivered_at) VALUES('upsert','old','body','2000-01-01','2000-01-01'),('upsert','pending','body','2000-01-01',NULL);
 INSERT INTO sync_runs(source_id,status,started_at,finished_at) VALUES('s','succeeded','2000-01-01','2000-01-01'),('s','running','2000-01-01',NULL);
 INSERT INTO audit_events(occurred_at,issuer,subject,credential_id,action,resource,status) VALUES(1,'i','s','','test','',200),(1,'i','s','','test','',0);`)
	if err != nil {
		t.Fatal(err)
	}
	policy := Retention{Outbox: time.Hour, SyncRuns: time.Hour}
	preview, err := st.Prune(ctx, policy, true)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Outbox != 1 || preview.SyncRuns != 1 || preview.Audit != 0 {
		t.Fatalf("preview=%v", preview)
	}
	applied, err := st.Prune(ctx, policy, false)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Outbox != preview.Outbox || applied.SyncRuns != preview.SyncRuns {
		t.Fatalf("apply=%v", applied)
	}
	pending, err := st.PendingOutbox(ctx, 100)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	policy.Audit = time.Hour
	applied, err = st.Prune(ctx, policy, false)
	if err != nil || applied.Audit != 1 {
		t.Fatalf("audit=%v err=%v", applied, err)
	}
	var count int
	if err := st.db.QueryRow(`SELECT count(*) FROM audit_events WHERE status=0`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("pending audit=%d %v", count, err)
	}
}
