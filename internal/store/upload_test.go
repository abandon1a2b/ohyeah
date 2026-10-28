package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/internal/source"
	"github.com/abandon1a2b/ohyeah/pkg/collector"
)

func uploadFixture(t *testing.T) (*Store, model.Source) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	registered := model.Source{ID: "project/remote/node", ProjectID: "project", WorkspaceID: "project", TypeID: "remote", Path: "/remote", Driver: model.DriverUpload, Enabled: true, State: model.SourceStateActive, Writer: "alice", TypeRevision: 1, MountRevision: 1}
	if err := s.UpsertSource(context.Background(), registered); err != nil {
		t.Fatal(err)
	}
	return s, registered
}

func uploadBatch(t *testing.T, registered model.Source, id string, version int64, empty bool) (collector.UploadBatch, model.ScanResult) {
	t.Helper()
	envelopes := []collector.Envelope{}
	if !empty {
		envelopes = append(envelopes, collector.Envelope{Type: "document", Document: &collector.Document{ExternalID: "document", Reference: map[string]string{"url": "https://evidence.invalid/one"}, Units: []collector.Unit{{Key: "final", Content: "verified conclusion", Title: "first"}}}})
	}
	envelopes = append(envelopes, collector.Envelope{Type: "complete", Mode: "snapshot", Cursor: json.RawMessage(`{"offset":1}`)})
	batch := collector.UploadBatch{ProtocolVersion: 1, SourceID: registered.ID, BatchID: id, ExpectedVersion: version, ScopeRevision: UploadScope(registered), Envelopes: envelopes}
	result, err := source.ParseEnvelopes(context.Background(), registered, envelopes)
	if err != nil {
		t.Fatal(err)
	}
	return batch, result
}

func TestUploadAtomicReplayAndIsolation(t *testing.T) {
	s, registered := uploadFixture(t)
	ctx := context.Background()
	batch, result := uploadBatch(t, registered, "first", 0, false)
	receipt, err := s.CommitUpload(ctx, "alice", "issuer", "token", batch, "hash", result)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Version != 1 || receipt.Changed != 1 {
		t.Fatal(receipt)
	}
	replay, err := s.CommitUpload(ctx, "alice", "issuer", "token", batch, "hash", result)
	if err != nil || !replay.Replayed || replay.RunID != receipt.RunID {
		t.Fatalf("replay: %#v %v", replay, err)
	}
	if _, err := s.CommitUpload(ctx, "alice", "issuer", "token", batch, "different", result); !errors.Is(err, ErrUploadConflict) {
		t.Fatal("batch ID reuse accepted")
	}
	if _, err := s.CommitUpload(ctx, "bob", "issuer", "token", batch, "hash", result); !errors.Is(err, ErrUploadSource) {
		t.Fatal("wrong writer accepted")
	}
	second, secondResult := uploadBatch(t, registered, "second", 0, false)
	if _, err := s.CommitUpload(ctx, "alice", "issuer", "token", second, "second", secondResult); !errors.Is(err, ErrUploadConflict) {
		t.Fatal("stale version accepted")
	}
	second.ExpectedVersion = 1
	second.ScopeRevision = "wrong"
	if _, err := s.CommitUpload(ctx, "alice", "issuer", "token", second, "second", secondResult); !errors.Is(err, ErrUploadConflict) {
		t.Fatal("stale scope accepted")
	}
	other := registered
	other.ID = "project/remote/other"
	if err := s.UpsertSource(ctx, other); err != nil {
		t.Fatal(err)
	}
	otherBatch, otherResult := uploadBatch(t, other, "first", 0, false)
	if _, err := s.CommitUpload(ctx, "alice", "issuer", "token", otherBatch, "other", otherResult); err != nil {
		t.Fatal(err)
	}
	deletion, deletionResult := uploadBatch(t, registered, "delete", 1, true)
	if _, err := s.CommitUpload(ctx, "alice", "issuer", "token", deletion, "delete", deletionResult); err != nil {
		t.Fatal(err)
	}
	if count, err := s.MemoryCount(ctx); err != nil || count != 1 {
		t.Fatalf("snapshot crossed source boundary: %d %v", count, err)
	}
	state, err := s.UploadState(ctx, registered)
	if err != nil || state.Version != 2 || string(state.Cursor) != `{"offset":1}` {
		t.Fatalf("state: %#v %v", state, err)
	}
	runs, err := s.ListSyncRuns(ctx, registered.ID, 10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("duplicate run after retry: %#v %v", runs, err)
	}
}

func TestConcurrentUploadCASAndRollback(t *testing.T) {
	s, registered := uploadFixture(t)
	ctx := context.Background()
	var winners atomic.Int32
	var group sync.WaitGroup
	for _, id := range []string{"one", "two"} {
		batch, result := uploadBatch(t, registered, id, 0, false)
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := s.CommitUpload(ctx, "alice", "issuer", "token", batch, id, result)
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, ErrUploadConflict) {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if winners.Load() != 1 {
		t.Fatalf("CAS winners: %d", winners.Load())
	}
	bad, result := uploadBatch(t, registered, "rollback", 1, false)
	result.Documents[0].Object.SourceID = "nonexistent"
	result.Documents[0].Object.ID = "new-invalid-object"
	result.Documents[0].Object.ContentHash = "changed"
	if _, err := s.CommitUpload(ctx, "alice", "issuer", "token", bad, "bad", result); err == nil {
		t.Fatal("invalid batch committed")
	}
	state, err := s.UploadState(ctx, registered)
	if err != nil || state.Version != 1 {
		t.Fatalf("version advanced on rollback: %#v %v", state, err)
	}
	valid, result := uploadBatch(t, registered, "rollback", 1, true)
	if _, err := s.CommitUpload(ctx, "alice", "issuer", "token", valid, "good", result); err != nil {
		t.Fatalf("failed batch ID was consumed: %v", err)
	}
}
