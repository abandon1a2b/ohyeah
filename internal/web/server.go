package web

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/app"
	"github.com/abandon1a2b/ohyeah/internal/auth"
	"github.com/abandon1a2b/ohyeah/internal/distribution"
	"github.com/abandon1a2b/ohyeah/internal/model"
	collectsource "github.com/abandon1a2b/ohyeah/internal/source"
	"github.com/abandon1a2b/ohyeah/internal/store"
	"github.com/abandon1a2b/ohyeah/internal/syncer"
	"github.com/abandon1a2b/ohyeah/pkg/collector"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

//go:embed dist
var embedded embed.FS

type configRequest struct {
	Raw          string `json:"raw"`
	ExpectedHash string `json:"expectedHash"`
}

func New(application *app.App) *fiber.App {
	configuration := application.Configuration
	if configuration == nil {
		configuration = app.NewConfiguration(application.Store, application.Config)
	}
	server := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		BodyLimit:             32 << 20,
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
	server.Use(recover.New())
	if application.Auth != nil {
		application.Auth.Register(server)
	}
	api := server.Group("/api/v1")
	api.Get("/health", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })
	api.Use(func(c *fiber.Ctx) error {
		if c.Path() != "/api/v1/uploads" && len(c.Body()) > 2<<20 {
			return fiber.ErrRequestEntityTooLarge
		}
		if application.Config.Auth.Mode == "oidc" && (application.Auth == nil || !application.Auth.Enabled()) {
			return fiber.ErrServiceUnavailable
		}
		if application.Auth == nil {
			c.Locals("localAuth", true)
			return c.Next()
		}
		return application.Auth.Authenticate(c)
	})

	api.Get("/status", func(c *fiber.Ctx) error {
		principal := auth.Current(c)
		result, err := application.Status(c.Context(), principal == nil || principal.Admin)
		if err != nil {
			return err
		}
		return c.JSON(result)
	})

	api.Get("/projects", func(c *fiber.Ctx) error {
		projects, err := application.Store.ListProjects(c.Context())
		if err != nil {
			return err
		}
		filtered := make([]model.Project, 0, len(projects))
		for _, project := range projects {
			if auth.Can(c, project.ID, "read") {
				filtered = append(filtered, project)
			}
		}
		return c.JSON(filtered)
	})
	api.Get("/sources", func(c *fiber.Ctx) error {
		sources, err := application.Store.ListSources(c.Context(), false)
		if err != nil {
			return err
		}
		filtered := make([]model.Source, 0, len(sources))
		for _, source := range sources {
			if auth.Can(c, source.ProjectID, "read") {
				if p := auth.Current(c); p != nil && !p.Admin {
					source.Options = nil
					source.Execution = model.CollectorExecution{}
					source.Cursor = nil
				}
				filtered = append(filtered, source)
			}
		}
		return c.JSON(filtered)
	})

	api.Get("/sync/runs", func(c *fiber.Ctx) error {
		var ids []string
		if p := auth.Current(c); p != nil && !p.Admin {
			ids = []string{}
			sources, err := application.Store.ListSources(c.Context(), false)
			if err != nil {
				return err
			}
			for _, source := range sources {
				if auth.Can(c, source.ProjectID, "read") && (c.Query("source") == "" || source.ID == c.Query("source")) {
					ids = append(ids, source.ID)
				}
			}
			if c.Query("source") != "" && len(ids) == 0 {
				return fiber.ErrNotFound
			}
		} else if sourceID := c.Query("source"); sourceID != "" {
			ids = []string{sourceID}
		}
		limit, err := strconv.Atoi(c.Query("limit", "50"))
		if err != nil || limit < 1 || limit > 200 {
			return fiber.NewError(400, "limit must be between 1 and 200")
		}
		runs, err := application.Store.ListSyncRunsForSources(c.Context(), ids, limit)
		if err != nil {
			return err
		}
		if runs == nil {
			runs = []model.SyncRun{}
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
			c.Locals("auditResource", request.SourceID)
			source, err := application.Store.GetSource(c.Context(), request.SourceID)
			if err != nil {
				return err
			}
			if !auth.CanSource(c, source, "sync") {
				return fiber.ErrForbidden
			}
			if source.Driver == model.DriverUpload {
				return fiber.NewError(409, "source expects uploaded batches, not server collector execution")
			}
			if !source.Enabled || source.State != model.SourceStateActive {
				return fiber.NewError(409, "source is disabled or detached")
			}
			if p := auth.Current(c); p != nil {
				slog.Info("sync requested", "subject", p.Subject, "source", source.ID)
			}
			run, err := application.Syncer.SyncByID(c.Context(), request.SourceID)
			if err != nil {
				return err
			}
			return c.JSON(run)
		}
		if err := auth.Require(c, "", "admin"); err != nil {
			return err
		}
		runs, err := application.Syncer.SyncAll(c.Context())
		if err != nil {
			return c.Status(fiber.StatusMultiStatus).JSON(fiber.Map{"runs": runs, "error": err.Error()})
		}
		return c.JSON(runs)
	})
	uploadSource := func(c *fiber.Ctx, id string) (model.Source, error) {
		source, err := application.Store.GetSource(c.Context(), id)
		if err != nil {
			return source, err
		}
		subject, _, _ := auth.Actor(c)
		writer := source.Writer
		if source.Driver != model.DriverUpload || !source.Enabled || source.State != model.SourceStateActive || !auth.CanSource(c, source, "upload") || writer != subject {
			return source, fiber.ErrForbidden
		}
		return source, nil
	}
	api.Get("/uploads/state", func(c *fiber.Ctx) error {
		source, err := uploadSource(c, c.Query("source"))
		if err != nil {
			return err
		}
		state, err := application.Store.UploadState(c.Context(), source)
		if errors.Is(err, store.ErrUploadConflict) {
			return fiber.NewError(409, err.Error())
		}
		if err != nil {
			return err
		}
		source.Cursor = state.Cursor
		return c.JSON(fiber.Map{"sourceId": state.SourceID, "version": state.Version, "scopeRevision": state.ScopeRevision, "cursor": state.Cursor, "request": collectsource.CollectorRequest(source, application.Config.Sync.MaxFileSize)})
	})
	api.Post("/uploads", func(c *fiber.Ctx) error {
		var batch collector.UploadBatch
		decoder := json.NewDecoder(bytes.NewReader(c.Body()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&batch); err != nil {
			return fiber.NewError(400, "invalid upload JSON: "+err.Error())
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return fiber.NewError(400, "upload must contain exactly one JSON object")
		}
		source, err := uploadSource(c, batch.SourceID)
		if err != nil {
			return err
		}
		c.Locals("auditResource", source.ID)
		subject, issuer, credential := auth.Actor(c)
		receipt, err := application.Syncer.Upload(c.Context(), source, subject, issuer, credential, batch)
		if errors.Is(err, store.ErrUploadConflict) {
			return fiber.NewError(409, err.Error())
		}
		if errors.Is(err, store.ErrUploadSource) {
			return fiber.ErrForbidden
		}
		if errors.Is(err, syncer.ErrInvalidUpload) {
			return fiber.NewError(422, err.Error())
		}
		if err != nil {
			return err
		}
		if receipt.IndexPending {
			return c.Status(202).JSON(receipt)
		}
		return c.JSON(receipt)
	})
	api.Get("/memories/:id", func(c *fiber.Ctx) error {
		record, err := application.Store.GetMemory(c.Context(), c.Params("id"))
		if err != nil {
			return err
		}
		if !auth.Can(c, record.Unit.ProjectID, "read") {
			return fiber.ErrNotFound
		}
		return c.JSON(record)
	})
	api.Get("/search", func(c *fiber.Ctx) error {
		if application.Config.Auth.Mode == "oidc" && c.Query("project") == "" {
			return fiber.NewError(400, "project is required")
		}
		if err := auth.Require(c, c.Query("project"), "read"); err != nil {
			return err
		}
		query := strings.TrimSpace(c.Query("q"))
		if query == "" {
			return fiber.NewError(fiber.StatusBadRequest, "q is required")
		}
		limit, err := strconv.Atoi(c.Query("limit", "20"))
		if err != nil || limit < 1 || limit > 200 {
			return fiber.NewError(400, "limit must be between 1 and 200")
		}
		var kinds []string
		for _, kind := range strings.Split(c.Query("kind"), ",") {
			if value := strings.TrimSpace(kind); value != "" {
				kinds = append(kinds, value)
			}
		}
		mode := c.Query("mode")
		if mode != "" && mode != "keyword" && mode != "hybrid" && mode != "semantic" {
			return fiber.NewError(fiber.StatusBadRequest, "mode must be keyword, hybrid or semantic")
		}
		hits, err := application.Syncer.Search(c.Context(), model.SearchQuery{Query: query, Mode: mode, ProjectID: c.Query("project"), TypeID: c.Query("type"), MountID: c.Query("mount"), WorkspaceID: c.Query("workspace"), Kinds: kinds, Limit: limit})
		if err != nil {
			return err
		}
		return c.JSON(hits)
	})
	api.Get("/index/doctor", func(c *fiber.Ctx) error {
		if err := auth.Require(c, "", "admin"); err != nil {
			return err
		}
		result, err := application.Syncer.Doctor(c.Context())
		if err != nil {
			return err
		}
		return c.JSON(result)
	})
	api.Post("/index/rebuild", func(c *fiber.Ctx) error {
		if err := auth.Require(c, "", "admin"); err != nil {
			return err
		}
		count, err := application.Syncer.RebuildIndex(c.Context())
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"rebuilt": true, "documents": count})
	})

	api.Get("/config", func(c *fiber.Ctx) error {
		if err := auth.Require(c, "", "admin"); err != nil {
			return err
		}
		snapshot, err := configuration.Read(c.Context())
		if err != nil {
			return err
		}
		return c.JSON(snapshot)
	})
	api.Post("/config/validate", func(c *fiber.Ctx) error {
		if err := auth.Require(c, "", "admin"); err != nil {
			return err
		}
		var request configRequest
		if err := c.BodyParser(&request); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		result, err := configuration.Validate(c.Context(), request.Raw)
		if err != nil {
			return configurationError(err)
		}
		return c.JSON(result)
	})
	api.Put("/config", func(c *fiber.Ctx) error {
		if err := auth.Require(c, "", "admin"); err != nil {
			return err
		}
		var request configRequest
		if err := c.BodyParser(&request); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		result, err := configuration.Save(c.Context(), request.Raw, request.ExpectedHash)
		if err != nil {
			if errors.Is(err, app.ErrConfigurationConflict) && len(result.Actions) > 0 {
				return c.Status(409).JSON(fiber.Map{"error": err.Error(), "actions": result.Actions})
			}
			return configurationError(err)
		}
		return c.JSON(result)
	})

	publicURL := ""
	if application.Config.Auth.Mode == "oidc" {
		publicURL = application.Config.Auth.PublicURL
	}
	distribution.Register(server, application.Config.Server.Address, publicURL)
	assets, _ := fs.Sub(embedded, "dist")
	server.Use(filesystem.New(filesystem.Config{Root: http.FS(assets), Browse: false, Index: "index.html", NotFoundFile: "index.html"}))
	return server
}

func Run(ctx context.Context, application *app.App, address string) error {
	security, err := auth.New(ctx, application.Config, application.Store)
	if err != nil {
		return err
	}
	copy := *application
	copy.Auth = security
	application = &copy
	server := New(application)
	errs := make(chan error, 1)
	go func() { errs <- server.Listen(address) }()
	select {
	case <-ctx.Done():
		if err := server.ShutdownWithTimeout(5 * time.Second); err != nil {
			return err
		}
		return ctx.Err()
	case err := <-errs:
		return err
	}
}

func configurationError(err error) error {
	if errors.Is(err, app.ErrConfigurationConflict) {
		return fiber.NewError(409, err.Error())
	}
	if errors.Is(err, app.ErrInvalidConfiguration) {
		return fiber.NewError(422, err.Error())
	}
	return err
}
