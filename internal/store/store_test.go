package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

func TestReplaceDocumentCreatesRecoverableOutbox(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	registered := model.Source{ID: "docs", WorkspaceID: "test", Driver: model.DriverCommand, Path: t.TempDir(), Enabled: true}
	if err := state.UpsertSource(ctx, registered); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	document := model.Document{
		Object: model.SourceObject{ID: "object-1", SourceID: "docs", ExternalID: "a.md", ContentHash: "hash", ModifiedAt: now, ParserVersion: 1, Reference: map[string]string{"path": "/docs/a.md", "aliases": "/docs/old-a.md"}},
		Units: []model.MemoryUnit{{
			ID: "unit-1", WorkspaceID: "test", SourceID: "docs", SourceType: "fixture",
			Kind: model.KindDocument, Title: "Title", Content: "Content", OccurredAt: now,
			ContentHash: "unit-hash", Status: model.StatusObserved,
		}},
	}
	if err := state.ReplaceDocument(ctx, document); err != nil {
		t.Fatal(err)
	}
	unit, err := state.GetUnit(ctx, "unit-1")
	if err != nil {
		t.Fatal(err)
	}
	if unit.ObjectID != "object-1" {
		t.Fatalf("object id = %q", unit.ObjectID)
	}
	record, err := state.GetMemory(ctx, "unit-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Reference["aliases"] != "/docs/old-a.md" {
		t.Fatalf("reference = %#v", record.Reference)
	}
	outbox, err := state.PendingOutbox(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 1 || outbox[0].Operation != "upsert" || outbox[0].UnitID != "unit-1" {
		t.Fatalf("outbox = %#v", outbox)
	}
}

func TestConcurrentReadOnlyOpenDoesNotLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	initial, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}

	const readers = 12
	errorsByReader := make(chan error, readers)
	var group sync.WaitGroup
	for range readers {
		group.Add(1)
		go func() {
			defer group.Done()
			state, err := Open(path)
			if err != nil {
				errorsByReader <- err
				return
			}
			defer state.Close()
			_, err = state.Stats(context.Background())
			errorsByReader <- err
		}()
	}
	group.Wait()
	close(errorsByReader)
	for err := range errorsByReader {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSourceCursorRoundTrip(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	registered := model.Source{
		ID: "custom", WorkspaceID: "test", Driver: model.DriverCommand, Path: t.TempDir(), Enabled: true,
		Options: map[string]any{"command": "collector", "args": []string{"--mode", "fast"}},
	}
	if err := state.UpsertSource(ctx, registered); err != nil {
		t.Fatal(err)
	}
	if err := state.UpdateSourceCursor(ctx, registered.ID, json.RawMessage(`{"offset":9}`)); err != nil {
		t.Fatal(err)
	}
	loaded, err := state.GetSource(ctx, registered.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.Cursor) != `{"offset":9}` {
		t.Fatalf("cursor = %s", loaded.Cursor)
	}
	if loaded.Options["command"] != "collector" {
		t.Fatalf("options = %#v", loaded.Options)
	}
}

func TestApplyScanResultRollsBackWholeMount(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	registered := model.Source{ID: "mount", WorkspaceID: "test", Driver: model.DriverCommand, Path: t.TempDir(), Enabled: true}
	if err := state.UpsertSource(ctx, registered); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	result := model.ScanResult{Documents: []model.Document{
		{
			Object: model.SourceObject{ID: "object-ok", SourceID: "mount", ExternalID: "ok", ContentHash: "ok", ModifiedAt: now, ParserVersion: 1},
			Units:  []model.MemoryUnit{{ID: "unit-ok", WorkspaceID: "test", SourceID: "mount", SourceType: "fixture", Kind: model.KindDocument, Title: "ok", Content: "ok", OccurredAt: now, ContentHash: "ok", Status: model.StatusObserved}},
		},
		{
			Object: model.SourceObject{ID: "object-bad", SourceID: "missing-source", ExternalID: "bad", ContentHash: "bad", ModifiedAt: now, ParserVersion: 1},
		},
	}, Cursor: json.RawMessage(`{"offset":9}`), PruneMissing: true}
	if _, _, err := state.ApplyScanResult(ctx, registered.ID, result); err == nil {
		t.Fatal("expected foreign-key failure")
	}
	stats, err := state.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats["objects"] != 0 || stats["memoryUnits"] != 0 || stats["pendingOutbox"] != 0 {
		t.Fatalf("partial mount state was committed: %#v", stats)
	}
	loaded, err := state.GetSource(ctx, registered.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Cursor) != 0 {
		t.Fatalf("cursor advanced after rollback: %s", loaded.Cursor)
	}
}

func TestApplyConfigStateIsAtomic(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	change := ConfigStateChange{
		Projects:  []model.Project{{ID: "valid", Workspace: t.TempDir(), Enabled: true}},
		DataTypes: []model.DataType{{ID: "missing/type", ProjectID: "missing", Name: "type", Command: "/collector", Revision: 1, ConfigHash: "hash", Enabled: true}},
	}
	if err := state.ApplyConfigState(ctx, change); err == nil {
		t.Fatal("expected foreign-key failure")
	}
	projects, err := state.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("partial config state was committed: %#v", projects)
	}
}
