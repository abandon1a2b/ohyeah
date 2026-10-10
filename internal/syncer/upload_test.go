package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/internal/store"
	"github.com/abandon1a2b/ohyeah/pkg/collector"
)

type outageBackend struct {
	recordingBackend
	unavailable bool
}

func (b *outageBackend) Ensure(context.Context) error {
	if b.unavailable {
		return errors.New("index unavailable")
	}
	return nil
}

func TestUploadSurvivesIndexOutageAndServiceRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	state, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	registered := model.Source{ID: "project/remote/node", ProjectID: "project", WorkspaceID: "project", Driver: model.DriverUpload, Path: "/remote", Enabled: true, State: model.SourceStateActive, Options: map[string]any{"__ohyeah_writer": "alice"}}
	if err := state.UpsertSource(ctx, registered); err != nil {
		t.Fatal(err)
	}
	backend := &outageBackend{unavailable: true}
	service := New(state, backend, config.Config{Meilisearch: config.MeilisearchConfig{BatchSize: 100}})
	batch := collector.UploadBatch{ProtocolVersion: 1, SourceID: registered.ID, BatchID: "durable", ExpectedVersion: 0, ScopeRevision: store.UploadScope(registered), Envelopes: []collector.Envelope{{Type: "document", Document: &collector.Document{ExternalID: "one", Units: []collector.Unit{{Key: "one", Title: "before", Content: "evidence"}}}}, {Type: "complete", Mode: "snapshot", Cursor: json.RawMessage(`{"offset":7}`)}}}
	receipt, err := service.Upload(ctx, registered, "alice", "issuer", "token", batch)
	if err != nil || !receipt.IndexPending || receipt.Version != 1 {
		t.Fatalf("outage receipt: %#v %v", receipt, err)
	}
	if pending, err := state.PendingOutbox(ctx, 100); err != nil || len(pending) != 1 {
		t.Fatalf("outbox not durable: %#v %v", pending, err)
	}
	state.Close()
	state, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	backend.unavailable = false
	service = New(state, backend, config.Config{Meilisearch: config.MeilisearchConfig{BatchSize: 100}})
	replay, err := service.Upload(ctx, registered, "alice", "issuer", "token", batch)
	if err != nil || !replay.Replayed || replay.IndexPending || replay.RunID != receipt.RunID {
		t.Fatalf("restart replay: %#v %v", replay, err)
	}
	if pending, err := state.PendingOutbox(ctx, 100); err != nil || len(pending) != 0 {
		t.Fatalf("outbox not recovered: %#v %v", pending, err)
	}
	current, err := state.GetSource(ctx, registered.ID)
	if err != nil || string(current.Cursor) != `{"offset":7}` {
		t.Fatalf("cursor not durable: %#v %v", current, err)
	}
	batch.BatchID = "metadata-update"
	batch.ExpectedVersion = 1
	batch.Envelopes[0].Document.Units[0].Title = "after"
	updated, err := service.Upload(ctx, current, "alice", "issuer", "token", batch)
	if err != nil || updated.Changed != 1 {
		t.Fatalf("title update skipped: %#v %v", updated, err)
	}
}
