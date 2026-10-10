package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

func TestSystemAuditDoesNotLeakToSameNamedPrincipal(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	registered := model.Source{ID: "private-source", WorkspaceID: "private", Driver: model.DriverCommand, Path: "/private", Enabled: true}
	if err := s.UpsertSource(ctx, registered); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ApplyScanResult(ctx, registered.ID, model.ScanResult{}, AuditEvent{Issuer: "issuer", Subject: "system", ActorKind: "system", Action: "collector.commit"}); err != nil {
		t.Fatal(err)
	}
	own, err := s.ListAudit(ctx, "issuer", "system", false, 0, 50)
	if err != nil || len(own) != 0 {
		t.Fatalf("system audit leaked to identically named user: %#v %v", own, err)
	}
	all, err := s.ListAudit(ctx, "issuer", "admin", true, 0, 50)
	if err != nil || len(all) != 1 || all[0].ActorKind != "system" {
		t.Fatalf("administrator cannot inspect system event: %#v %v", all, err)
	}
}
