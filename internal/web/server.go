package web

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/abandon1a2b/ohyeah/internal/app"
	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/internal/registry"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
)

//go:embed dist
var embedded embed.FS

type configRequest struct {
	Raw          string `json:"raw"`
	ExpectedHash string `json:"expectedHash"`
}

func New(application *app.App) *fiber.App {
	server := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		BodyLimit:             2 << 20,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			var fiberErr *fiber.Error
			if errors.As(err, &fiberErr) {
				code = fiberErr.Code
			} else if errors.Is(err, sql.ErrNoRows) || errors.Is(err, fs.ErrNotExist) {
				code = fiber.StatusNotFound
			}
			return c.Status(code).JSON(fiber.Map{"error": err.Error()})
		},
	})
	api := server.Group("/api/v1")
	api.Get("/health", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })
	api.Get("/status", func(c *fiber.Ctx) error {
		stats, err := application.Store.Stats(c.Context())
		if err != nil {
			return err
		}
		backend := "available"
		if err := application.Backend.Healthy(c.Context()); err != nil {
			backend = err.Error()
		}
		doctor, doctorErr := application.Syncer.Doctor(c.Context())
		return c.JSON(fiber.Map{"backend": backend, "stats": stats, "statePath": application.Config.StatePath, "index": application.Config.Meilisearch.Index, "doctor": doctor, "doctorError": errorString(doctorErr)})
	})
	api.Get("/projects", func(c *fiber.Ctx) error {
		projects, err := application.Store.ListProjects(c.Context())
		if err != nil {
			return err
		}
		return c.JSON(projects)
	})
	api.Get("/sources", func(c *fiber.Ctx) error {
		sources, err := application.Store.ListSources(c.Context(), false)
		if err != nil {
			return err
		}
		return c.JSON(sources)
	})
	api.Get("/sync/runs", func(c *fiber.Ctx) error {
		limit, _ := strconv.Atoi(c.Query("limit", "50"))
		runs, err := application.Store.ListSyncRuns(c.Context(), c.Query("source"), limit)
		if err != nil {
			return err
		}
		return c.JSON(runs)
	})
	api.Post("/sync", func(c *fiber.Ctx) error {
		var request struct {
			SourceID string `json:"sourceId"`
		}
		if len(c.Body()) > 0 {
			if err := c.BodyParser(&request); err != nil {
				return fiber.NewError(fiber.StatusBadRequest, err.Error())
			}
		}
		if request.SourceID != "" {
			run, err := application.Syncer.SyncByID(c.Context(), request.SourceID)
			if err != nil {
				return err
			}
			return c.JSON(run)
		}
		runs, err := application.Syncer.SyncAll(c.Context())
		if err != nil {
			return c.Status(fiber.StatusMultiStatus).JSON(fiber.Map{"runs": runs, "error": err.Error()})
		}
		return c.JSON(runs)
	})
	api.Get("/memories/:id", func(c *fiber.Ctx) error {
		record, err := application.Store.GetMemory(c.Context(), c.Params("id"))
		if err != nil {
			return err
		}
		return c.JSON(record)
	})
	api.Get("/search", func(c *fiber.Ctx) error {
		query := strings.TrimSpace(c.Query("q"))
		if query == "" {
			return fiber.NewError(fiber.StatusBadRequest, "q is required")
		}
		limit, _ := strconv.Atoi(c.Query("limit", "20"))
		var kinds []string
		for _, kind := range strings.Split(c.Query("kind"), ",") {
			if value := strings.TrimSpace(kind); value != "" {
				kinds = append(kinds, value)
			}
		}
		hits, err := application.Syncer.Search(c.Context(), model.SearchQuery{Query: query, ProjectID: c.Query("project"), TypeID: c.Query("type"), MountID: c.Query("mount"), WorkspaceID: c.Query("workspace"), Kinds: kinds, Limit: limit})
		if err != nil {
			return err
		}
		return c.JSON(hits)
	})
	api.Get("/index/doctor", func(c *fiber.Ctx) error {
		result, err := application.Syncer.Doctor(c.Context())
		if err != nil {
			return err
		}
		return c.JSON(result)
	})
	api.Post("/index/rebuild", func(c *fiber.Ctx) error {
		count, err := application.Syncer.RebuildIndex(c.Context())
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"rebuilt": true, "documents": count})
	})

	api.Get("/config", func(c *fiber.Ctx) error {
		raw, err := readConfig(application.Config.Path)
		if err != nil {
			return err
		}
		cfg, err := config.Load(application.Config.Path)
		if err != nil {
			return err
		}
		actions, err := registry.New(application.Store, cfg).Plan(c.Context())
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"path": application.Config.Path, "raw": string(raw), "hash": contentHash(raw), "projects": cfg.Projects, "actions": changedActions(actions)})
	})
	api.Post("/config/validate", func(c *fiber.Ctx) error {
		var request configRequest
		if err := c.BodyParser(&request); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		cfg, cleanup, err := loadCandidate(application.Config.Path, request.Raw)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return fiber.NewError(fiber.StatusUnprocessableEntity, err.Error())
		}
		actions, err := registry.New(application.Store, cfg).Plan(c.Context())
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"valid": true, "actions": changedActions(actions)})
	})
	api.Put("/config", func(c *fiber.Ctx) error {
		var request configRequest
		if err := c.BodyParser(&request); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		current, err := readConfig(application.Config.Path)
		if err != nil {
			return err
		}
		if request.ExpectedHash != "" && request.ExpectedHash != contentHash(current) {
			return fiber.NewError(fiber.StatusConflict, "configuration changed on disk; reload before saving")
		}
		cfg, cleanup, err := loadCandidate(application.Config.Path, request.Raw)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return fiber.NewError(fiber.StatusUnprocessableEntity, err.Error())
		}
		actions, err := registry.New(application.Store, cfg).Plan(c.Context())
		if err != nil {
			return err
		}
		for _, action := range actions {
			if action.Type == registry.ActionConflict {
				return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "configuration has conflicts", "actions": changedActions(actions)})
			}
		}
		if err := writeConfigAtomic(application.Config.Path, []byte(request.Raw)); err != nil {
			return err
		}
		applied, err := registry.New(application.Store, cfg).Apply(c.Context())
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"saved": true, "hash": contentHash([]byte(request.Raw)), "actions": changedActions(applied)})
	})

	assets, _ := fs.Sub(embedded, "dist")
	server.Use(filesystem.New(filesystem.Config{Root: http.FS(assets), Browse: false, Index: "index.html", NotFoundFile: "index.html"}))
	return server
}

func Run(ctx context.Context, application *app.App, address string) error {
	server := New(application)
	errs := make(chan error, 1)
	go func() { errs <- server.Listen(address) }()
	select {
	case <-ctx.Done():
		if err := server.Shutdown(); err != nil {
			return err
		}
		return ctx.Err()
	case err := <-errs:
		return err
	}
}

func loadCandidate(activePath, raw string) (config.Config, func(), error) {
	if activePath == "" {
		return config.Config{}, nil, errors.New("serve requires a configuration file for web editing")
	}
	file, err := os.CreateTemp(filepath.Dir(activePath), ".ohyeah-config-*.yaml")
	if err != nil {
		return config.Config{}, nil, err
	}
	cleanup := func() { _ = os.Remove(file.Name()) }
	if _, err := file.WriteString(raw); err != nil {
		_ = file.Close()
		cleanup()
		return config.Config{}, nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return config.Config{}, nil, err
	}
	cfg, err := config.Load(file.Name())
	return cfg, cleanup, err
}

func readConfig(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("no configuration file is active")
	}
	return os.ReadFile(path)
}

func writeConfigAtomic(path string, raw []byte) error {
	if path == "" {
		return errors.New("no configuration file is active")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".ohyeah-config-write-*.yaml")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(info.Mode().Perm()); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func changedActions(actions []registry.Action) []registry.Action {
	result := make([]registry.Action, 0, len(actions))
	for _, action := range actions {
		if action.Type != registry.ActionUnchanged {
			result = append(result, action)
		}
	}
	return result
}

func contentHash(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
