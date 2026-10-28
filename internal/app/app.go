package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/gofrs/flock"
	"path/filepath"

	"github.com/abandon1a2b/ohyeah/internal/auth"
	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/managed"
	"github.com/abandon1a2b/ohyeah/internal/search"
	"github.com/abandon1a2b/ohyeah/internal/store"
	"github.com/abandon1a2b/ohyeah/internal/syncer"
)

type App struct {
	stateLock     *flock.Flock
	Configuration *Configuration
	Config        config.Config
	Store         *store.Store
	Backend       search.Backend
	Syncer        *syncer.Service
	Auth          *auth.Service
}

func Open(configPath string) (*App, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if err := config.EnsureDirectories(cfg); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	startup := cfg
	cfg.StatePath, err = filepath.Abs(cfg.StatePath)
	if err != nil {
		return nil, err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(cfg.StatePath); resolveErr == nil {
		cfg.StatePath = resolved
	} else if parent, resolveErr := filepath.EvalSymlinks(filepath.Dir(cfg.StatePath)); resolveErr == nil {
		cfg.StatePath = filepath.Join(parent, filepath.Base(cfg.StatePath))
	}
	stateLock := flock.New(cfg.StatePath + ".lock")
	locked, err := stateLock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, errors.New("state is in use; use HTTP clients while serve is running, or stop it before local maintenance")
	}
	opened := false
	defer func() {
		if !opened {
			_ = stateLock.Unlock()
		}
	}()
	if err := managed.Resolve(&cfg.Meilisearch); err != nil {
		return nil, fmt.Errorf("prepare managed Meilisearch: %w", err)
	}
	state, err := store.Open(cfg.StatePath)
	if err != nil {
		return nil, fmt.Errorf("open state store: %w", err)
	}
	backend := search.NewMeilisearch(cfg.Meilisearch)
	opened = true
	return &App{stateLock: stateLock, Configuration: NewConfiguration(state, startup), Config: cfg, Store: state, Backend: backend, Syncer: syncer.New(state, backend, cfg)}, nil
}

func (a *App) Close() error {
	err := a.Store.Close()
	if a.stateLock != nil {
		err = errors.Join(err, a.stateLock.Unlock())
	}
	return err
}

func (a *App) Ensure(ctx context.Context) error { return a.Backend.Ensure(ctx) }
