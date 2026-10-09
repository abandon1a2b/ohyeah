package syncer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/internal/store"
)

type vectorBackend struct {
	recordingBackend
	documents map[string]model.MemoryUnit
}

func (b *vectorBackend) ResetVectors(context.Context) error {
	b.documents = make(map[string]model.MemoryUnit)
	return nil
}
func (b *vectorBackend) EnsureVectors(context.Context) error {
	if b.documents == nil {
		b.documents = make(map[string]model.MemoryUnit)
	}
	return nil
}
func (b *vectorBackend) VectorDocuments(context.Context) (map[string]model.MemoryUnit, error) {
	result := make(map[string]model.MemoryUnit, len(b.documents))
	for id, unit := range b.documents {
		result[id] = unit
	}
	return result, nil
}
func (b *vectorBackend) UpsertVectors(_ context.Context, payloads [][]byte) error {
	for _, payload := range payloads {
		var unit model.MemoryUnit
		if err := json.Unmarshal(payload, &unit); err != nil {
			return err
		}
		unit.Content = ""
		b.documents[unit.ID] = unit
	}
	return nil
}
func (b *vectorBackend) DeleteVectors(_ context.Context, ids []string) error {
	for _, id := range ids {
		delete(b.documents, id)
	}
	return nil
}

func TestSyncVectorIndexIsIncremental(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	source := model.Source{ID: "docs", WorkspaceID: "test", Driver: model.DriverCommand, Path: t.TempDir(), Enabled: true}
	if err := state.UpsertSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	backend := &vectorBackend{}
	service := New(state, backend, config.Config{})
	writeDocument := func(hash, title string) {
		t.Helper()
		now := time.Now().UTC()
		if err := state.ReplaceDocument(ctx, model.Document{
			Object: model.SourceObject{ID: "object", SourceID: source.ID, ExternalID: "one", ContentHash: hash, ModifiedAt: now},
			Units:  []model.MemoryUnit{{ID: "unit", WorkspaceID: "test", SourceID: source.ID, SourceType: "fixture", Kind: model.KindDocument, Title: title, Content: "body", OccurredAt: now, ContentHash: hash, Status: model.StatusObserved}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	writeDocument("v1", "first")
	check := func(rebuild, dryRun bool, updated, unchanged, deleted int64) {
		t.Helper()
		result, err := service.SyncVectorIndex(ctx, rebuild, dryRun)
		if err != nil {
			t.Fatal(err)
		}
		if result != (VectorSyncResult{Updated: updated, Unchanged: unchanged, Deleted: deleted}) {
			t.Fatalf("vector result = %#v", result)
		}
	}
	check(false, true, 1, 0, 0)
	if len(backend.documents) != 0 {
		t.Fatal("dry run modified vector index")
	}
	check(false, false, 1, 0, 0)
	check(false, false, 0, 1, 0)
	check(true, false, 1, 0, 0)
	writeDocument("v2", "second")
	check(false, true, 1, 0, 0)
	check(false, false, 1, 0, 0)
	if backend.documents["unit"].Title != "second" {
		t.Fatal("changed vector metadata was not updated")
	}
	if _, err := state.DeleteObjects(ctx, source.ID, []string{"one"}); err != nil {
		t.Fatal(err)
	}
	check(false, false, 0, 0, 1)
	check(false, false, 0, 0, 0)
}

type recordingBackend struct {
	mu    sync.Mutex
	count int64
}

func (b *recordingBackend) Ensure(context.Context) error { return nil }
func (b *recordingBackend) Reset(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.count = 0
	return nil
}
func (b *recordingBackend) Upsert(_ context.Context, payloads [][]byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.count += int64(len(payloads))
	return nil
}
func (b *recordingBackend) Delete(_ context.Context, ids []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.count -= int64(len(ids))
	return nil
}
func (b *recordingBackend) Search(context.Context, model.SearchQuery) ([]model.SearchHit, error) {
	return nil, nil
}
func (b *recordingBackend) Healthy(context.Context) error { return nil }
func (b *recordingBackend) Count(context.Context) (int64, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count, true, nil
}

func TestSyncAllContinuesAfterCollectorFailure(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "bad.sh")
	good := filepath.Join(root, "good.sh")
	if err := os.WriteFile(bad, []byte("#!/bin/sh\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	goodScript := `#!/bin/sh
read request
printf '%s\n' '{"type":"document","document":{"externalId":"one","sourceType":"fixture","units":[{"key":"answer","kind":"conclusion","title":"answer","content":"good","status":"confirmed"}]}}'
printf '%s\n' '{"type":"complete","mode":"snapshot"}'
`
	if err := os.WriteFile(good, []byte(goodScript), 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	for _, source := range []model.Source{
		{ID: "a-bad", WorkspaceID: "test", Driver: model.DriverCommand, Path: root, Enabled: true, Options: map[string]any{"command": bad}},
		{ID: "b-good", WorkspaceID: "test", Driver: model.DriverCommand, Path: root, Enabled: true, Options: map[string]any{"command": good}},
	} {
		if err := state.UpsertSource(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	backend := &recordingBackend{}
	service := New(state, backend, config.Config{
		Meilisearch: config.MeilisearchConfig{BatchSize: 100},
		Sync:        config.SyncConfig{MaxFileSize: 1 << 20, CollectorTimeout: 5 * time.Second},
	})
	runs, err := service.SyncAll(ctx)
	if err == nil {
		t.Fatal("expected aggregated collector error")
	}
	if len(runs) != 2 {
		t.Fatalf("runs=%#v err=%v", runs, err)
	}
	count, err := state.MemoryCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("good mount was not synchronized after failure: count=%d", count)
	}
	backendCount, _, _ := backend.Count(ctx)
	if backendCount != 1 {
		t.Fatalf("successful mount was not dispatched: count=%d", backendCount)
	}
}
