package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/registry"
	"github.com/abandon1a2b/ohyeah/internal/store"
)

var ErrConfigurationConflict = errors.New("configuration conflict; reload before saving")
var ErrInvalidConfiguration = errors.New("invalid configuration")

// Configuration serializes configuration operations within a service instance.
// External editors and separate CLI processes are not covered by this lock.
// startup remains immutable: only registry state is reloaded without restart.
type Configuration struct {
	mu      sync.Mutex
	state   *store.Store
	startup config.Config
}

func NewConfiguration(state *store.Store, startup config.Config) *Configuration {
	return &Configuration{state: state, startup: startup}
}

type ConfigurationResult struct {
	Path            string                          `json:"path"`
	Raw             string                          `json:"raw"`
	Hash            string                          `json:"hash"`
	Projects        map[string]config.ProjectConfig `json:"projects"`
	Actions         []registry.Action               `json:"actions"`
	RestartRequired []string                        `json:"restartRequired"`
	Valid           bool                            `json:"valid"`
	Saved           bool                            `json:"saved"`
}

func (s *Configuration) Read(ctx context.Context) (ConfigurationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := readConfig(s.startup.Path)
	if err != nil {
		return ConfigurationResult{}, err
	}
	result, _, err := s.validate(ctx, string(raw))
	result.Path, result.Raw, result.Hash = s.startup.Path, string(raw), contentHash(raw)
	return result, err
}

func (s *Configuration) Validate(ctx context.Context, raw string) (ConfigurationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, _, err := s.validate(ctx, raw)
	return result, err
}

func (s *Configuration) validate(ctx context.Context, raw string) (ConfigurationResult, config.Config, error) {
	cfg, err := config.Parse(s.startup.Path, raw)
	if err != nil {
		return ConfigurationResult{}, cfg, fmt.Errorf("%w: %v", ErrInvalidConfiguration, err)
	}
	actions, err := registry.New(s.state, cfg).Plan(ctx)
	if err != nil {
		return ConfigurationResult{}, cfg, err
	}
	result := ConfigurationResult{Projects: cfg.Projects, Actions: changedActions(actions), RestartRequired: restartRequired(s.startup, cfg), Valid: true}
	for _, action := range actions {
		if action.Type == registry.ActionConflict {
			result.Valid = false
		}
	}
	return result, cfg, nil
}

func (s *Configuration) Save(ctx context.Context, raw, expectedHash string) (ConfigurationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := readConfig(s.startup.Path)
	if err != nil {
		return ConfigurationResult{}, err
	}
	if expectedHash != "" && expectedHash != contentHash(current) {
		return ConfigurationResult{}, ErrConfigurationConflict
	}
	result, cfg, err := s.validate(ctx, raw)
	if err != nil {
		return result, err
	}
	if !result.Valid {
		return result, ErrConfigurationConflict
	}
	if err := writeConfigAtomic(s.startup.Path, []byte(raw)); err != nil {
		return result, err
	}
	result.Saved, result.Hash = true, contentHash([]byte(raw))
	actions, err := registry.New(s.state, cfg).Apply(ctx)
	if err != nil {
		// The file is durable even if SQLite application fails; Reload retries it.
		return result, fmt.Errorf("configuration saved but runtime application failed: %w", err)
	}
	result.Actions = changedActions(actions)
	return result, nil
}

func (s *Configuration) Apply(ctx context.Context, cfg config.Config) ([]registry.Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return registry.New(s.state, cfg).Apply(ctx)
}

func (s *Configuration) Reload(ctx context.Context) ([]registry.Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := config.Load(s.startup.Path)
	if err != nil {
		return nil, err
	}
	return registry.New(s.state, cfg).Apply(ctx)
}

// Only names are exposed, never changed values (which may contain credentials).
func restartRequired(startup, candidate config.Config) []string {
	result := []string{}
	for _, field := range []struct {
		name          string
		before, after any
	}{
		{"state_path", startup.StatePath, candidate.StatePath},
		{"server", startup.Server, candidate.Server},
		{"auth", startup.Auth, candidate.Auth},
		{"meilisearch", startup.Meilisearch, candidate.Meilisearch},
		{"sync", startup.Sync, candidate.Sync},
		{"maintenance", startup.Maintenance, candidate.Maintenance},
		{"log_level", startup.LogLevel, candidate.LogLevel},
	} {
		if !reflect.DeepEqual(field.before, field.after) {
			result = append(result, field.name)
		}
	}
	return result
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

func contentHash(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}
