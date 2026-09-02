package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/model"
	meilisearch "github.com/meilisearch/meilisearch-go"
)

type Meilisearch struct {
	client meilisearch.ServiceManager
	index  string
}

func NewMeilisearch(cfg config.MeilisearchConfig) *Meilisearch {
	options := []meilisearch.Option{}
	if cfg.APIKey != "" {
		options = append(options, meilisearch.WithAPIKey(cfg.APIKey))
	}
	return &Meilisearch{client: meilisearch.New(cfg.URL, options...), index: cfg.Index}
}

func (m *Meilisearch) Ensure(ctx context.Context) error {
	if err := m.Healthy(ctx); err != nil {
		return err
	}
	_, err := m.client.GetIndex(m.index)
	if err != nil {
		if !isNotFound(err) {
			return fmt.Errorf("get Meilisearch index: %w", err)
		}
		task, createErr := m.client.CreateIndex(&meilisearch.IndexConfig{Uid: m.index, PrimaryKey: "id"})
		if createErr != nil {
			return fmt.Errorf("create Meilisearch index: %w", createErr)
		}
		if err := m.wait(task.TaskUID); err != nil {
			return err
		}
	}
	index := m.client.Index(m.index)
	current, err := index.GetSettings()
	if err != nil {
		return fmt.Errorf("get Meilisearch settings: %w", err)
	}
	searchable := []string{"title", "requirementIds", "entities", "heading", "path", "content"}
	filterable := []string{"projectId", "typeId", "mountId", "workspaceId", "sourceType", "sourceId", "kind", "status", "threadId", "requirementIds", "repos"}
	sortable := []string{"occurredAt"}
	settings := []struct {
		needed bool
		name   string
		call   func() (*meilisearch.TaskInfo, error)
	}{
		{!slices.Equal(current.SearchableAttributes, searchable), "searchable attributes", func() (*meilisearch.TaskInfo, error) {
			return index.UpdateSearchableAttributes(&searchable)
		}},
		{!slices.Equal(current.FilterableAttributes, filterable), "filterable attributes", func() (*meilisearch.TaskInfo, error) {
			values := make([]interface{}, len(filterable))
			for index, value := range filterable {
				values[index] = value
			}
			return index.UpdateFilterableAttributes(&values)
		}},
		{!slices.Equal(current.SortableAttributes, sortable), "sortable attributes", func() (*meilisearch.TaskInfo, error) {
			return index.UpdateSortableAttributes(&sortable)
		}},
	}
	for _, setting := range settings {
		if !setting.needed {
			continue
		}
		task, err := setting.call()
		if err != nil {
			return fmt.Errorf("update %s: %w", setting.name, err)
		}
		if err := m.wait(task.TaskUID); err != nil {
			return fmt.Errorf("update %s: %w", setting.name, err)
		}
	}
	return nil
}

func (m *Meilisearch) Reset(ctx context.Context) error {
	_, err := m.client.GetIndex(m.index)
	if err != nil {
		if isNotFound(err) {
			return m.Ensure(ctx)
		}
		return err
	}
	task, err := m.client.DeleteIndex(m.index)
	if err != nil {
		return err
	}
	if err := m.wait(task.TaskUID); err != nil {
		return err
	}
	return m.Ensure(ctx)
}

func (m *Meilisearch) Upsert(ctx context.Context, payloads [][]byte) error {
	if len(payloads) == 0 {
		return nil
	}
	documents := bytes.Join(payloads, []byte("\n"))
	task, err := m.client.Index(m.index).AddDocumentsNdjson(documents, nil)
	if err != nil {
		return err
	}
	return m.wait(task.TaskUID)
}

func (m *Meilisearch) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	task, err := m.client.Index(m.index).DeleteDocuments(ids, nil)
	if err != nil {
		return err
	}
	return m.wait(task.TaskUID)
}

func (m *Meilisearch) Search(ctx context.Context, query model.SearchQuery) ([]model.SearchHit, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	filters := make([]string, 0, 2)
	if query.WorkspaceID != "" {
		filters = append(filters, `workspaceId = "`+escapeFilter(query.WorkspaceID)+`"`)
	}
	if query.ProjectID != "" {
		filters = append(filters, `projectId = "`+escapeFilter(query.ProjectID)+`"`)
	}
	if query.TypeID != "" {
		filters = append(filters, `typeId = "`+escapeFilter(query.TypeID)+`"`)
	}
	if query.MountID != "" {
		filters = append(filters, `mountId = "`+escapeFilter(query.MountID)+`"`)
	}
	if len(query.Kinds) > 0 {
		values := make([]string, 0, len(query.Kinds))
		for _, kind := range query.Kinds {
			values = append(values, `"`+escapeFilter(kind)+`"`)
		}
		filters = append(filters, `kind IN [`+strings.Join(values, ",")+`]`)
	}
	request := &meilisearch.SearchRequest{
		Limit:                int64(limit * 4),
		AttributesToRetrieve: []string{"id", "objectId", "projectId", "typeId", "mountId", "workspaceId", "sourceId", "sourceType", "kind", "title", "threadId", "turnId", "path", "heading", "occurredAt", "contentHash", "status", "requirementIds", "repos", "entities", "metadata"},
		AttributesToCrop:     []string{"content"},
		CropLength:           80,
		ShowRankingScore:     true,
		Sort:                 []string{"occurredAt:desc"},
	}
	if len(filters) > 0 {
		request.Filter = strings.Join(filters, " AND ")
	}
	response, err := m.client.Index(m.index).Search(query.Query, request)
	if err != nil {
		return nil, err
	}
	hits := make([]model.SearchHit, 0, len(response.Hits))
	for _, raw := range response.Hits {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		var unit model.MemoryUnit
		if err := json.Unmarshal(encoded, &unit); err != nil {
			return nil, err
		}
		var ranked struct {
			Score     float64 `json:"_rankingScore"`
			Formatted struct {
				Content string `json:"content"`
			} `json:"_formatted"`
		}
		_ = json.Unmarshal(encoded, &ranked)
		unit.Content = ranked.Formatted.Content
		hits = append(hits, model.SearchHit{Unit: unit, Score: ranked.Score})
	}
	return hits, nil
}

func (m *Meilisearch) Count(ctx context.Context) (int64, bool, error) {
	stats, err := m.client.Index(m.index).GetStatsWithContext(ctx, nil)
	if err != nil {
		if isNotFound(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return stats.NumberOfDocuments, true, nil
}

func (m *Meilisearch) Healthy(ctx context.Context) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		health, err := m.client.Health()
		if err == nil && health.Status == "available" {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			return fmt.Errorf("Meilisearch health is %q", health.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (m *Meilisearch) wait(taskID int64) error {
	task, err := m.client.WaitForTask(taskID, 50*time.Millisecond)
	if err != nil {
		return err
	}
	if task.Status == meilisearch.TaskStatusFailed {
		return fmt.Errorf("Meilisearch task %d failed: %v", taskID, task.Error)
	}
	return nil
}

func escapeFilter(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
}

func isNotFound(err error) bool {
	var meiliErr *meilisearch.Error
	return errors.As(err, &meiliErr) && meiliErr.StatusCode == 404
}
