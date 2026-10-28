package syncer

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/internal/store"
)

type stateBackend struct{ vectorBackend }

func (b *stateBackend) Upsert(ctx context.Context, units []model.MemoryUnit) error {
	return b.UpsertVectors(ctx, units)
}
func (b *stateBackend) Delete(ctx context.Context, ids []string) error {
	return b.DeleteVectors(ctx, ids)
}

func TestDeleteRecreateAndCorrectionSurviveUpdates(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertSource(ctx, model.Source{ID: "s", WorkspaceID: "w", Driver: model.DriverCommand, Path: t.TempDir(), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	backend := &stateBackend{}
	backend.EnsureVectors(ctx)
	svc := New(st, backend, config.Config{Meilisearch: config.MeilisearchConfig{BatchSize: 100}})
	doc := func(id, hash string) model.Document {
		return model.Document{Object: model.SourceObject{ID: id, SourceID: "s", ExternalID: id, ContentHash: hash}, Units: []model.MemoryUnit{{ID: id + "-unit", SourceID: "s", WorkspaceID: "w", ContentHash: hash}}}
	}
	a, b := doc("a", "v1"), doc("b", "v1")
	a.Relations = []model.Relation{{ID: "r", FromID: "a-unit", ToID: "b-unit", Type: model.RelationCorrects}}
	commit := func(docs ...model.Document) {
		t.Helper()
		if _, _, err := st.ApplyScanResult(ctx, "s", model.ScanResult{Documents: docs}); err != nil {
			t.Fatal(err)
		}
	}
	commit(a, b)
	if err := svc.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	b.Object.ContentHash = "v2"
	commit(a, b)
	corrections, err := st.CorrectingUnits(ctx, []string{"b-unit"})
	if err != nil || len(corrections["b-unit"]) != 1 {
		t.Fatalf("correction lost: %v %v", corrections, err)
	}
	if _, err := st.DeleteObjects(ctx, "s", []string{"b"}); err != nil {
		t.Fatal(err)
	}
	commit(a, b)
	corrections, err = st.CorrectingUnits(ctx, []string{"b-unit"})
	if err != nil || len(corrections["b-unit"]) != 1 {
		t.Fatalf("recreated target lost declared correction: %v %v", corrections, err)
	}
	if err := svc.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found := backend.documents["b-unit"]; !found {
		t.Fatal("recreated record deleted from index")
	}
	if _, err := svc.Search(ctx, model.SearchQuery{Query: "x", Limit: -1}); err == nil {
		t.Fatal("negative search limit accepted")
	}
}
