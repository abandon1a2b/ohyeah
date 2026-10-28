// Package runtime owns the lifetime of the HTTP server and background workers.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"github.com/abandon1a2b/ohyeah/internal/store"
	"log/slog"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/app"
	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/managed"
	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/internal/syncer"
	"github.com/abandon1a2b/ohyeah/internal/web"
)

// Run starts all workers, cancels siblings when one exits, and waits for them
// before returning. The caller owns and closes application after Run returns.
func Run(parent context.Context, application *app.App) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var err error
	var engine *managed.Process
	if application.Config.Meilisearch.Mode == "managed" {
		engine, err = managed.Start(ctx, application.Config.Meilisearch)
		if err != nil {
			return err
		}
		defer engine.Close()
		if err := application.Ensure(ctx); err != nil {
			return err
		}
		doctor, err := application.Syncer.Doctor(ctx)
		if err != nil {
			return err
		}
		if !doctor.Consistent {
			if _, err := application.Syncer.RebuildIndex(ctx); err != nil {
				return err
			}
		}
	}
	if _, err := application.Configuration.Apply(ctx, application.Config); err != nil {
		return err
	}
	sources, err := application.Store.ListSources(ctx, true)
	if err != nil {
		return err
	}

	workers := []worker{
		{"maintenance", func(ctx context.Context) error {
			cfg := application.Config.Maintenance
			ticker := time.NewTicker(cfg.Interval)
			defer ticker.Stop()
			for {
				if _, err := application.Store.Prune(ctx, store.Retention{Outbox: cfg.OutboxRetention, SyncRuns: cfg.SyncRetention, Audit: cfg.AuditRetention}, false); err != nil && ctx.Err() == nil {
					slog.Warn("retention cleanup failed", "error", err)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
				}
			}
		}},
		{"synchronization", application.Syncer.RunPeriodic},
		{"source watcher", func(ctx context.Context) error {
			return syncer.WatchDynamic(ctx, func(ctx context.Context) ([]model.Source, error) {
				return application.Store.ListSources(ctx, true)
			}, application.Config.Sync.Debounce, func(ctx context.Context) error {
				_, err := application.Syncer.SyncAll(ctx)
				return err
			})
		}},
		{"configuration watcher", func(ctx context.Context) error {
			return config.Watch(ctx, application.Config.Path, application.Config.Sync.Debounce, func(ctx context.Context) error {
				_, err := application.Configuration.Reload(ctx)
				return err
			})
		}},
		{"HTTP server", func(ctx context.Context) error { return web.Run(ctx, application, application.Config.Server.Address) }},
	}
	if engine != nil {
		workers = append(workers, worker{"Meilisearch", engine.Wait})
	}
	slog.Info("ohyeah service started", "sources", len(sources), "web", "http://"+application.Config.Server.Address)
	return runWorkers(ctx, workers)
}

type worker struct {
	name string
	run  func(context.Context) error
}

func runWorkers(parent context.Context, workers []worker) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	results := make(chan error, len(workers))
	for _, w := range workers {
		go func() {
			err := w.run(ctx)
			if err == nil && ctx.Err() == nil {
				err = errors.New("worker stopped unexpectedly")
			}
			if err != nil {
				err = fmt.Errorf("%s: %w", w.name, err)
			}
			results <- err
		}()
	}
	var failures []error
	for range workers {
		err := <-results
		if err != nil && !(ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))) {
			failures = append(failures, err)
		}
		cancel()
	}
	return errors.Join(failures...)
}
