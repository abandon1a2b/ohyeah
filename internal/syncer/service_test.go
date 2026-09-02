package syncer

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/internal/store"
)

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
