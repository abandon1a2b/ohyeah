package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/model"
)

func TestSearchModes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		enabled    bool
		mode       string
		ratio      float64
		wantHybrid bool
		wantError  bool
	}{
		{"legacy default", false, "", 0, false, false},
		{"configured default remains keyword", true, "", 0, false, false},
		{"explicit hybrid", true, "hybrid", 0.5, true, false},
		{"explicit keyword", true, "keyword", 0, false, false},
		{"pure semantic", true, "semantic", 1, true, false},
		{"disabled semantic falls back", false, "semantic", 0, false, false},
		{"disabled hybrid falls back", false, "hybrid", 0, false, false},
		{"invalid mode", true, "other", 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := NewMeilisearch(config.MeilisearchConfig{URL: "http://127.0.0.1:7700", Embedding: config.EmbeddingConfig{Enabled: tc.enabled, Name: "qwen", SemanticRatio: 0.5}})
			hybrid, err := backend.searchMode(tc.mode)
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v", err)
			}
			if (hybrid != nil) != tc.wantHybrid {
				t.Fatalf("hybrid = %#v", hybrid)
			}
			if hybrid != nil && (hybrid.Embedder != "qwen" || hybrid.SemanticRatio != tc.ratio) {
				t.Fatalf("hybrid = %#v", hybrid)
			}
		})
	}
}

func TestQwenIntegration(t *testing.T) {
	if os.Getenv("OHYEAH_TEST_QWEN") != "1" {
		t.Skip("requires local Meilisearch and Ollama Qwen")
	}
	ctx := context.Background()
	backend := NewMeilisearch(config.MeilisearchConfig{URL: "http://127.0.0.1:7700", Index: fmt.Sprintf("ohyeah_qwen_test_%d", time.Now().UnixNano()), Embedding: config.EmbeddingConfig{Enabled: true, Name: "qwen", URL: "http://127.0.0.1:11434/api/embed", Model: "qwen3-embedding:0.6b", Dimensions: 1024, DocumentTemplateMaxBytes: 12000, SemanticRatio: 0.5}})
	t.Cleanup(func() {
		vectorTask, err := backend.client.DeleteIndex(backend.vectorIndex())
		if err == nil {
			_ = backend.wait(vectorTask.TaskUID)
		}
		task, err := backend.client.DeleteIndex(backend.index)
		if err == nil {
			err = backend.wait(task.TaskUID)
		}
		if err != nil {
			t.Errorf("cleanup index: %v", err)
		}
	})
	if err := backend.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	// Repeated initialization must leave the primary index independent of models.
	if err := backend.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	embedders, err := backend.client.Index(backend.index).GetEmbeddersWithContext(ctx)
	if err != nil || len(embedders) != 0 {
		t.Fatalf("primary index embedders = %#v, error = %v", embedders, err)
	}
	var payloads [][]byte
	for _, unit := range []model.MemoryUnit{
		{ID: "db", ProjectID: "test", Title: "Database pool", Content: "Requests time out because database connections are exhausted. Close unused connections.", OccurredAt: time.Now()},
		{ID: "css", ProjectID: "test", Title: "Mobile layout", Content: "Wrap long button labels on small screens.", OccurredAt: time.Now()},
		{ID: "other", ProjectID: "other", Title: "Database pool", Content: "Requests time out because database connections are exhausted.", OccurredAt: time.Now()},
	} {
		payload, err := json.Marshal(unit)
		if err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, payload)
	}
	if err := backend.Upsert(ctx, payloads); err != nil {
		t.Fatal(err)
	}
	if err := backend.ResetVectors(ctx); err != nil {
		t.Fatal(err)
	}
	if err := backend.UpsertVectors(ctx, payloads); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"hybrid", "semantic"} {
		hits, err := backend.Search(ctx, model.SearchQuery{Query: "数据库连接用完导致请求超时", ProjectID: "test", Mode: mode, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) == 0 || hits[0].Unit.ID != "db" {
			t.Fatalf("mode %q hits = %#v", mode, hits)
		}
		for _, hit := range hits {
			if hit.Unit.ProjectID != "test" {
				t.Fatalf("project filter leaked: %#v", hit)
			}
		}
	}
	hits, err := backend.Search(ctx, model.SearchQuery{Query: "database", Mode: "keyword", ProjectID: "test", Limit: 2})
	if err != nil || len(hits) == 0 || hits[0].Unit.ID != "db" {
		t.Fatalf("keyword hits = %#v, error = %v", hits, err)
	}
	// An unavailable model must not affect initialization, writes, deletes or searches.
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer unavailable.Close()
	backend.embedding.URL = unavailable.URL
	if err := backend.ResetVectors(ctx); err == nil {
		t.Fatal("vector rebuild must fail before deleting the last index when the model is unavailable")
	}
	stats, err := backend.client.Index(backend.vectorIndex()).GetStatsWithContext(ctx, nil)
	if err != nil || stats.NumberOfDocuments != 3 {
		t.Fatalf("failed rebuild removed last vector index: stats=%#v error=%v", stats, err)
	}
	if err := backend.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := backend.Upsert(ctx, payloads); err != nil {
		t.Fatal(err)
	}
	if err := backend.Delete(ctx, []string{"css"}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "keyword", "hybrid", "semantic"} {
		hits, err := backend.Search(ctx, model.SearchQuery{Query: "database", Mode: mode, ProjectID: "test", Limit: 2})
		if err != nil || len(hits) == 0 || hits[0].Unit.ID != "db" {
			t.Fatalf("outage mode %q: hits=%#v error=%v", mode, hits, err)
		}
	}
	if err := backend.UpsertVectors(ctx, payloads); err == nil {
		t.Fatal("explicit vector build should report model failure")
	}
}

func TestOptionalSearchFallsBackWhenVectorIndexMissing(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/embed" {
			fmt.Fprint(w, `{"embeddings":[[1,0]]}`)
			return
		}
		requests++
		if strings.HasPrefix(r.URL.Path, "/indexes/test_vectors_") {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"code":"index_not_found","message":"missing","type":"invalid_request"}`)
			return
		}
		var req struct {
			Hybrid any `json:"hybrid"`
			Vector any `json:"vector"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.Hybrid != nil || req.Vector != nil {
			t.Error("keyword fallback sent vector parameters")
		}
		fmt.Fprint(w, `{"hits":[{"id":"db","title":"database","_formatted":{"content":"connections"}}],"processingTimeMs":0,"query":"database","estimatedTotalHits":1}`)
	}))
	defer server.Close()
	backend := NewMeilisearch(config.MeilisearchConfig{URL: server.URL, Index: "test", Embedding: config.EmbeddingConfig{Enabled: true, Name: "qwen", URL: server.URL + "/embed", Model: "test", Dimensions: 2, SemanticRatio: 0.5}})
	hits, err := backend.Search(context.Background(), model.SearchQuery{Query: "database", Mode: "hybrid", Limit: 1})
	if err != nil || len(hits) != 1 || requests != 2 {
		t.Fatalf("hits=%#v error=%v requests=%d", hits, err, requests)
	}
}

func TestVectorIndexChangesWithModelConfiguration(t *testing.T) {
	backend := &Meilisearch{index: "test", embedding: config.EmbeddingConfig{Name: "qwen", Model: "model-a", Dimensions: 1024, DocumentTemplateMaxBytes: 12000}}
	old := backend.vectorIndex()
	backend.embedding.Model = "model-b"
	if backend.vectorIndex() == old {
		t.Fatal("same-dimensional model change reused incompatible vectors")
	}
	old = backend.vectorIndex()
	backend.embedding.DocumentTemplateMaxBytes = 400
	if backend.vectorIndex() == old {
		t.Fatal("embedding input change reused old index")
	}
}

func TestOptionalSearchRejectsStaleMetadata(t *testing.T) {
	for _, change := range []string{"project", "content", "deleted"} {
		t.Run(change, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/embed":
					fmt.Fprint(w, `{"embeddings":[[1,0]]}`)
				case strings.HasPrefix(r.URL.Path, "/indexes/test_vectors_"):
					fmt.Fprint(w, `{"hits":[{"id":"db","projectId":"old","contentHash":"v1","_formatted":{"content":"old content"}}]}`)
				case r.URL.Path == "/indexes/test/documents/db":
					if change == "deleted" {
						w.WriteHeader(404)
						fmt.Fprint(w, `{"code":"document_not_found","message":"gone","type":"invalid_request"}`)
						return
					}
					project, hash := "old", "v1"
					if change == "project" {
						project = "new"
					} else {
						hash = "v2"
					}
					fmt.Fprintf(w, `{"id":"db","projectId":%q,"contentHash":%q,"content":"live content"}`, project, hash)
				case r.URL.Path == "/indexes/test/search":
					fmt.Fprint(w, `{"hits":[{"id":"fresh","_formatted":{"content":"fresh content"}}]}`)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			}))
			defer server.Close()
			backend := NewMeilisearch(config.MeilisearchConfig{URL: server.URL, Index: "test", Embedding: config.EmbeddingConfig{Enabled: true, Name: "qwen", Model: "test", Dimensions: 2, SemanticRatio: 0.5, URL: server.URL + "/embed"}})
			hits, err := backend.Search(context.Background(), model.SearchQuery{Query: "database", Mode: "hybrid"})
			if err != nil || len(hits) != 1 || hits[0].Unit.ID != "fresh" {
				t.Fatalf("stale metadata accepted: hits=%#v error=%v", hits, err)
			}
		})
	}
}
