package app

import (
	"context"
	"fmt"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/search"
	"github.com/abandon1a2b/ohyeah/internal/store"
	"github.com/abandon1a2b/ohyeah/internal/syncer"
)

type App struct {
	Config  config.Config
	Store   *store.Store
	Backend search.Backend
	Syncer  *syncer.Service
}

func Open(configPath string) (*App, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if err := config.EnsureDirectories(cfg); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	state, err := store.Open(cfg.StatePath)
	if err != nil {
		return nil, fmt.Errorf("open state store: %w", err)
	}
	backend := search.NewMeilisearch(cfg.Meilisearch)
	return &App{Config: cfg, Store: state, Backend: backend, Syncer: syncer.New(state, backend, cfg)}, nil
}

func (a *App) Close() error { return a.Store.Close() }

func (a *App) Ensure(ctx context.Context) error { return a.Backend.Ensure(ctx) }
