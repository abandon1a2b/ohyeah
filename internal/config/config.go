package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Path             string                   `mapstructure:"-"`
	StatePath        string                   `mapstructure:"state_path"`
	LogLevel         string                   `mapstructure:"log_level"`
	Meilisearch      MeilisearchConfig        `mapstructure:"meilisearch"`
	Sync             SyncConfig               `mapstructure:"sync"`
	Projects         map[string]ProjectConfig `mapstructure:"projects"`
	Server           ServerConfig             `mapstructure:"server"`
	HasProjectConfig bool                     `mapstructure:"-"`
}

type ServerConfig struct {
	Address string `mapstructure:"address"`
}

type MeilisearchConfig struct {
	URL       string `mapstructure:"url"`
	APIKey    string `mapstructure:"api_key"`
	Index     string `mapstructure:"index"`
	BatchSize int    `mapstructure:"batch_size"`
}

type SyncConfig struct {
	ReconcileInterval time.Duration `mapstructure:"reconcile_interval"`
	Debounce          time.Duration `mapstructure:"debounce"`
	CollectorTimeout  time.Duration `mapstructure:"collector_timeout"`
	MaxFileSize       int64         `mapstructure:"max_file_size"`
}

type ProjectConfig struct {
	Enabled   *bool                     `mapstructure:"enabled"`
	Workspace string                    `mapstructure:"workspace"`
	Types     map[string]DataTypeConfig `mapstructure:"types"`
}

type DataTypeConfig struct {
	Enabled   *bool                  `mapstructure:"enabled"`
	Collector CollectorConfig        `mapstructure:"collector"`
	Mounts    map[string]MountConfig `mapstructure:"mounts"`
}

type CollectorConfig struct {
	Command  string        `mapstructure:"command"`
	Args     []string      `mapstructure:"args"`
	Revision int           `mapstructure:"revision"`
	Timeout  time.Duration `mapstructure:"timeout"`
}

type MountConfig struct {
	Enabled  *bool          `mapstructure:"enabled"`
	Root     string         `mapstructure:"root"`
	Revision int            `mapstructure:"revision"`
	Options  map[string]any `mapstructure:"options"`
}

type ConfiguredMount struct {
	ProjectID string
	Workspace string
	TypeID    string
	Type      DataTypeConfig
	MountID   string
	Mount     MountConfig
}

var configIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func Load(path string) (Config, error) {
	v := viper.New()
	v.SetEnvPrefix("OHYEAH")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	dataDir, err := dataDir()
	if err != nil {
		return Config{}, err
	}

	v.SetDefault("state_path", filepath.Join(dataDir, "state.db"))
	v.SetDefault("log_level", "info")
	v.SetDefault("meilisearch.url", "http://127.0.0.1:7700")
	v.SetDefault("meilisearch.api_key", "")
	v.SetDefault("meilisearch.index", "ohyeah_memory_v1")
	v.SetDefault("meilisearch.batch_size", 100)
	v.SetDefault("sync.reconcile_interval", "15m")
	v.SetDefault("sync.debounce", "2s")
	v.SetDefault("sync.collector_timeout", "5m")
	v.SetDefault("sync.max_file_size", 256<<20)
	v.SetDefault("server.address", "127.0.0.1:8787")

	if path != "" {
		v.SetConfigFile(path)
	} else {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return Config{}, err
		}
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(filepath.Join(configDir, "ohyeah"))
	}

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if path != "" || !errors.As(err, &notFound) {
			return Config{}, err
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, err
	}
	cfg.HasProjectConfig = v.IsSet("projects")
	cfg.Path = v.ConfigFileUsed()
	if cfg.Path == "" && path != "" {
		cfg.Path = path
	}
	if cfg.Path != "" {
		cfg.Path, _ = filepath.Abs(cfg.Path)
	}
	if strings.TrimSpace(cfg.Server.Address) == "" {
		return Config{}, errors.New("server.address is required")
	}
	if cfg.Meilisearch.BatchSize <= 0 {
		return Config{}, errors.New("meilisearch.batch_size must be positive")
	}
	if cfg.Sync.ReconcileInterval <= 0 {
		return Config{}, errors.New("sync.reconcile_interval must be positive")
	}
	if cfg.Sync.MaxFileSize <= 0 {
		return Config{}, errors.New("sync.max_file_size must be positive")
	}
	if cfg.Sync.CollectorTimeout <= 0 {
		return Config{}, errors.New("sync.collector_timeout must be positive")
	}
	if err := cfg.ValidateProjects(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) ValidateProjects() error {
	for projectID, project := range c.Projects {
		projectEnabled := enabled(project.Enabled)
		if !configIDPattern.MatchString(projectID) {
			return fmt.Errorf("project id %q must match %s", projectID, configIDPattern)
		}
		if strings.TrimSpace(project.Workspace) == "" || !filepath.IsAbs(project.Workspace) {
			return fmt.Errorf("project %q workspace must be an absolute path", projectID)
		}
		if info, err := os.Stat(project.Workspace); err != nil || !info.IsDir() {
			return fmt.Errorf("project %q workspace is not a directory: %s", projectID, project.Workspace)
		}
		for typeID, dataType := range project.Types {
			typeEnabled := projectEnabled && enabled(dataType.Enabled)
			if !configIDPattern.MatchString(typeID) {
				return fmt.Errorf("type id %q in project %q must match %s", typeID, projectID, configIDPattern)
			}
			if dataType.Collector.Revision <= 0 {
				return fmt.Errorf("type %s/%s collector.revision must be positive", projectID, typeID)
			}
			if dataType.Collector.Timeout < 0 {
				return fmt.Errorf("type %s/%s collector.timeout cannot be negative", projectID, typeID)
			}
			if strings.TrimSpace(dataType.Collector.Command) == "" {
				return fmt.Errorf("type %s/%s requires collector.command", projectID, typeID)
			}
			command := resolveCommand(project.Workspace, dataType.Collector.Command)
			if typeEnabled {
				if strings.ContainsRune(command, filepath.Separator) {
					if info, err := os.Stat(command); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
						return fmt.Errorf("type %s/%s collector.command is not executable: %s", projectID, typeID, command)
					}
				} else if _, err := exec.LookPath(command); err != nil {
					return fmt.Errorf("type %s/%s collector.command not found in PATH: %s", projectID, typeID, command)
				}
			}
			for mountID, mount := range dataType.Mounts {
				if !configIDPattern.MatchString(mountID) {
					return fmt.Errorf("mount id %q in type %s/%s must match %s", mountID, projectID, typeID, configIDPattern)
				}
				if mount.Revision <= 0 {
					return fmt.Errorf("mount %s/%s/%s revision must be positive", projectID, typeID, mountID)
				}
				if strings.TrimSpace(mount.Root) == "" || !filepath.IsAbs(mount.Root) {
					return fmt.Errorf("mount %s/%s/%s root must be an absolute path", projectID, typeID, mountID)
				}
				if info, err := os.Stat(mount.Root); err != nil || !info.IsDir() {
					return fmt.Errorf("mount %s/%s/%s root is not a directory: %s", projectID, typeID, mountID, mount.Root)
				}
				if _, reserved := mount.Options["command"]; reserved {
					return fmt.Errorf("mount %s/%s/%s options cannot contain reserved key command", projectID, typeID, mountID)
				}
				if _, reserved := mount.Options["args"]; reserved {
					return fmt.Errorf("mount %s/%s/%s options cannot contain reserved key args", projectID, typeID, mountID)
				}
			}
		}
	}
	return nil
}

func (c Config) ConfiguredMounts() []ConfiguredMount {
	projectIDs := make([]string, 0, len(c.Projects))
	for projectID := range c.Projects {
		projectIDs = append(projectIDs, projectID)
	}
	sort.Strings(projectIDs)
	var result []ConfiguredMount
	for _, projectID := range projectIDs {
		project := c.Projects[projectID]
		typeIDs := sortedKeys(project.Types)
		for _, typeID := range typeIDs {
			dataType := project.Types[typeID]
			mountIDs := sortedKeys(dataType.Mounts)
			for _, mountID := range mountIDs {
				mount := dataType.Mounts[mountID]
				if !enabled(project.Enabled) || !enabled(dataType.Enabled) {
					disabled := false
					mount.Enabled = &disabled
				}
				result = append(result, ConfiguredMount{
					ProjectID: projectID, Workspace: project.Workspace,
					TypeID: typeID, Type: dataType, MountID: mountID, Mount: mount,
				})
			}
		}
	}
	return result
}

func resolveCommand(workspace, command string) string {
	if filepath.IsAbs(command) || !strings.ContainsRune(command, filepath.Separator) {
		return command
	}
	return filepath.Join(workspace, command)
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func Enabled(value *bool) bool { return enabled(value) }

func enabled(value *bool) bool {
	return value == nil || *value
}

func EnsureDirectories(cfg Config) error {
	return os.MkdirAll(filepath.Dir(cfg.StatePath), 0o700)
}

func dataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ohyeah"), nil
}
