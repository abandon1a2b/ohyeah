package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abandon1a2b/ohyeah/internal/model"
	meilisearch "github.com/meilisearch/meilisearch-go"
)

func (m *Meilisearch) vectorIndex() string {
	// Equal dimensions do not make embeddings from different models compatible.
	fingerprint := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d", m.embedding.Name, m.embedding.Model, m.embedding.Dimensions, m.embedding.DocumentTemplateMaxBytes)))
	return fmt.Sprintf("%s_vectors_%x", m.index, fingerprint[:8])
}

// ResetVectors is explicit maintenance. Regular initialization and sync never call it.
func (m *Meilisearch) ResetVectors(ctx context.Context) error {
	if !m.embedding.Enabled {
		return fmt.Errorf("embedding is disabled")
	}
	// Keep the last vector index when the model is already unavailable at startup.
	if _, err := m.embed(ctx, []string{"embedding readiness check"}); err != nil {
		return fmt.Errorf("check embedding service before rebuild: %w", err)
	}
	vector := &Meilisearch{client: m.client, index: m.vectorIndex()}
	if err := vector.Reset(ctx); err != nil {
		return err
	}
	task, err := m.client.Index(vector.index).UpdateEmbeddersWithContext(ctx, map[string]meilisearch.Embedder{
		m.embedding.Name: {Source: meilisearch.UserProvidedEmbedderSource, Dimensions: m.embedding.Dimensions},
	})
	if err != nil {
		return err
	}
	return m.wait(task.TaskUID)
}

func (m *Meilisearch) EnsureVectors(ctx context.Context) error {
	if !m.embedding.Enabled {
		return fmt.Errorf("embedding is disabled")
	}
	if _, err := m.client.GetIndex(m.vectorIndex()); err != nil {
		if !isNotFound(err) {
			return err
		}
		return m.ResetVectors(ctx)
	}
	return nil
}

func (m *Meilisearch) VectorDocuments(ctx context.Context) (map[string]model.MemoryUnit, error) {
	const pageSize = 5000
	fields := []string{"id", "objectId", "projectId", "typeId", "mountId", "workspaceId", "sourceId", "sourceType", "kind", "title", "threadId", "turnId", "path", "heading", "occurredAt", "contentHash", "status", "requirementIds", "repos", "entities", "metadata"}
	documents := make(map[string]model.MemoryUnit)
	for offset := int64(0); ; offset += pageSize {
		var page meilisearch.DocumentsResult
		if err := m.client.Index(m.vectorIndex()).GetDocumentsWithContext(ctx, &meilisearch.DocumentsQuery{Offset: offset, Limit: pageSize, Fields: fields}, &page); err != nil {
			if isNotFound(err) && offset == 0 {
				return documents, nil
			}
			return nil, err
		}
		for _, raw := range page.Results {
			encoded, err := json.Marshal(raw)
			if err != nil {
				return nil, err
			}
			var unit model.MemoryUnit
			if err := json.Unmarshal(encoded, &unit); err != nil {
				return nil, err
			}
			documents[unit.ID] = unit
		}
		if offset+int64(len(page.Results)) >= page.Total || len(page.Results) == 0 {
			return documents, nil
		}
	}
}

func (m *Meilisearch) DeleteVectors(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	task, err := m.client.Index(m.vectorIndex()).DeleteDocumentsWithContext(ctx, ids, nil)
	if err != nil {
		return err
	}
	return m.wait(task.TaskUID)
}

func (m *Meilisearch) embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{"model": m.embedding.Model, "input": texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.embedding.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 2 * time.Minute}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding service returned HTTP %d", response.StatusCode)
	}
	var result struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embedding count mismatch")
	}
	for _, vector := range result.Embeddings {
		if len(vector) != m.embedding.Dimensions {
			return nil, fmt.Errorf("embedding dimensions mismatch")
		}
	}
	return result.Embeddings, nil
}

func (m *Meilisearch) UpsertVectors(ctx context.Context, payloads [][]byte) error {
	texts := make([]string, 0, len(payloads))
	documents := make([]map[string]any, 0, len(payloads))
	for _, payload := range payloads {
		var unit model.MemoryUnit
		var document map[string]any
		if err := json.Unmarshal(payload, &unit); err != nil {
			return err
		}
		if err := json.Unmarshal(payload, &document); err != nil {
			return err
		}
		text := unit.Title + "\n" + unit.Content
		if len(text) > m.embedding.DocumentTemplateMaxBytes {
			text = text[:m.embedding.DocumentTemplateMaxBytes]
			for !utf8.ValidString(text) {
				text = text[:len(text)-1]
			}
		}
		texts = append(texts, strings.TrimSpace(text))
		documents = append(documents, document)
	}
	if len(texts) == 0 {
		return nil
	}
	vectors, err := m.embed(ctx, texts)
	if err != nil {
		return err
	}
	for i := range documents {
		documents[i]["_vectors"] = map[string]any{m.embedding.Name: vectors[i]}
	}
	task, err := m.client.Index(m.vectorIndex()).AddDocumentsWithContext(ctx, documents, nil)
	if err != nil {
		return err
	}
	return m.wait(task.TaskUID)
}
