package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/abandon1a2b/ohyeah/internal/model"
)

func TestCollectionRejectsSourceChangedDuringScan(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	source := model.Source{ID: "s", WorkspaceID: "w", Driver: model.DriverCommand, Path: t.TempDir(), Enabled: true, State: model.SourceStateActive, MountRevision: 1}
	if err := st.UpsertSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	current := source
	current.MountRevision = 2
	if err := st.UpsertSource(ctx, current); err != nil {
		t.Fatal(err)
	}
	result := model.ScanResult{Documents: []model.Document{{Object: model.SourceObject{ID: "o", SourceID: "s", ExternalID: "doc", ContentHash: "v1"}, Units: []model.MemoryUnit{{ID: "u", SourceID: "s", WorkspaceID: "w"}}}}}
	if _, _, err := st.CommitCollection(ctx, source, result); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("stale scan accepted: %v", err)
	}
	if count, err := st.MemoryCount(ctx); err != nil || count != 0 {
		t.Fatalf("stale scan wrote data: %d %v", count, err)
	}
	if _, _, err := st.CommitCollection(ctx, current, result); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionChecksOptionsAndNormalizesEmptyArgs(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	source := model.Source{ID: "s", WorkspaceID: "w", Driver: model.DriverCommand, Path: t.TempDir(), Enabled: true, State: model.SourceStateActive, Execution: model.CollectorExecution{Args: []string{}}, Options: map[string]any{"count": 1}}
	if err := st.UpsertSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CommitCollection(ctx, source, model.ScanResult{}); err != nil {
		t.Fatalf("JSON-equivalent arguments rejected: %v", err)
	}
	changed := source
	changed.Options = map[string]any{"count": 2}
	if err := st.UpsertSource(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CommitCollection(ctx, source, model.ScanResult{}); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("stale options accepted: %v", err)
	}
}
