package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
	Auth             AuthConfig               `mapstructure:"auth"`
	HasProjectConfig bool                     `mapstructure:"-"`
}

type ServerConfig struct {
	Address string `mapstructure:"address"`
}

type MeilisearchConfig struct {
	Mode      string          `mapstructure:"mode"`
	Directory string          `mapstructure:"directory"`
	URL       string          `mapstructure:"url"`
	APIKey    string          `mapstructure:"api_key"`
	Index     string          `mapstructure:"index"`
	BatchSize int             `mapstructure:"batch_size"`
	Embedding EmbeddingConfig `mapstructure:"embedding"`
}

func (c MeilisearchConfig) ManagedAddress() (string, error) {
	u, err := url.Parse(c.URL)
	if err != nil {
		return "", err
	}
	ip := net.ParseIP(u.Hostname())
	port, portErr := strconv.Atoi(u.Port())
	if u.Scheme != "http" || ip == nil || !ip.IsLoopback() || portErr != nil || port < 1 || port > 65535 || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("managed Meilisearch url must be an HTTP loopback IP with an explicit port")
	}
	return u.Host, nil
}

type EmbeddingConfig struct {
	Enabled                  bool    `mapstructure:"enabled"`
	Name                     string  `mapstructure:"name"`
	URL                      string  `mapstructure:"url"`
	Model                    string  `mapstructure:"model"`
	Dimensions               int     `mapstructure:"dimensions"`
	DocumentTemplateMaxBytes int     `mapstructure:"document_template_max_bytes"`
	SemanticRatio            float64 `mapstructure:"semantic_ratio"`
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
	Mode     string        `mapstructure:"mode"`
	Command  string        `mapstructure:"command"`
	Args     []string      `mapstructure:"args"`
	Revision int           `mapstructure:"revision"`
	Timeout  time.Duration `mapstructure:"timeout"`
}

type MountConfig struct {
	Writer   string         `mapstructure:"writer"`
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
	v.SetDefault("meilisearch.mode", "managed")
	v.SetDefault("meilisearch.directory", filepath.Join(dataDir, "meilisearch"))
	v.SetDefault("meilisearch.api_key", "")
	v.SetDefault("meilisearch.index", "ohyeah_memory_v1")
	v.SetDefault("meilisearch.batch_size", 100)
	v.SetDefault("meilisearch.embedding.enabled", false)
	v.SetDefault("meilisearch.embedding.name", "qwen")
	v.SetDefault("meilisearch.embedding.url", "http://127.0.0.1:11434/api/embed")
	v.SetDefault("meilisearch.embedding.model", "qwen3-embedding:0.6b")
	v.SetDefault("meilisearch.embedding.dimensions", 1024)
	v.SetDefault("meilisearch.embedding.document_template_max_bytes", 12000)
	v.SetDefault("meilisearch.embedding.semantic_ratio", 0.5)
	v.SetDefault("sync.reconcile_interval", "15m")
	v.SetDefault("sync.debounce", "2s")
	v.SetDefault("sync.collector_timeout", "5m")
	v.SetDefault("sync.max_file_size", 256<<20)
	v.SetDefault("server.address", "127.0.0.1:8787")
	v.SetDefault("auth.mode", "local")
	v.SetDefault("auth.session_ttl", "8h")
	v.SetDefault("auth.token_ttl", "24h")
	v.SetDefault("auth.oidc.client_secret", "")

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
	// Configurations written before managed mode explicitly selected an endpoint.
	_, envURL := os.LookupEnv("OHYEAH_MEILISEARCH_URL")
	_, envMode := os.LookupEnv("OHYEAH_MEILISEARCH_MODE")
	if !v.InConfig("meilisearch.mode") && !envMode && (v.InConfig("meilisearch.url") || envURL) {
		cfg.Meilisearch.Mode = "external"
	}
	if cfg.Meilisearch.Mode != "managed" && cfg.Meilisearch.Mode != "external" {
		return Config{}, errors.New("meilisearch.mode must be managed or external")
	}
	if cfg.Meilisearch.Mode == "managed" && !filepath.IsAbs(cfg.Meilisearch.Directory) {
		return Config{}, errors.New("meilisearch.directory must be an absolute path")
	}
	if cfg.Meilisearch.Mode == "managed" {
		if _, err := cfg.Meilisearch.ManagedAddress(); err != nil {
			return Config{}, err
		}
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
	if e := cfg.Meilisearch.Embedding; e.Enabled {
		if strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.URL) == "" || strings.TrimSpace(e.Model) == "" || e.Dimensions <= 0 || e.DocumentTemplateMaxBytes <= 0 {
			return Config{}, errors.New("enabled embedding requires name, url, model and positive dimensions/document_template_max_bytes")
		}
		if e.SemanticRatio <= 0 || e.SemanticRatio > 1 {
			return Config{}, errors.New("embedding.semantic_ratio must be greater than 0 and at most 1; use keyword mode for full-text search")
		}
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
	if err := cfg.ValidateAuth(); err != nil {
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
		needsLocal := false
		for _, dataType := range project.Types {
			if dataType.Collector.Mode != "upload" {
				needsLocal = true
			}
		}
		if info, err := os.Stat(project.Workspace); needsLocal && (err != nil || !info.IsDir()) {
			return fmt.Errorf("project %q workspace is not a directory: %s", projectID, project.Workspace)
		}
		for typeID, dataType := range project.Types {
			if dataType.Collector.Mode != "" && dataType.Collector.Mode != "command" && dataType.Collector.Mode != "upload" {
				return fmt.Errorf("type %s/%s collector.mode must be command or upload", projectID, typeID)
			}
			upload := dataType.Collector.Mode == "upload"
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
			if !upload && strings.TrimSpace(dataType.Collector.Command) == "" {
				return fmt.Errorf("type %s/%s requires collector.command", projectID, typeID)
			}
			command := resolveCommand(project.Workspace, dataType.Collector.Command)
			if upload && (dataType.Collector.Command != "" || len(dataType.Collector.Args) > 0) {
				return fmt.Errorf("upload type %s/%s cannot execute a command", projectID, typeID)
			}
			if typeEnabled && !upload {
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
				if upload && strings.TrimSpace(mount.Writer) == "" {
					return fmt.Errorf("upload mount %s/%s/%s requires writer subject", projectID, typeID, mountID)
				}
				if !upload && mount.Writer != "" {
					return fmt.Errorf("command mount %s/%s/%s cannot declare upload writer", projectID, typeID, mountID)
				}
				if info, err := os.Stat(mount.Root); !upload && (err != nil || !info.IsDir()) {
					return fmt.Errorf("mount %s/%s/%s root is not a directory: %s", projectID, typeID, mountID, mount.Root)
				}
				if _, reserved := mount.Options["command"]; reserved {
					return fmt.Errorf("mount %s/%s/%s options cannot contain reserved key command", projectID, typeID, mountID)
				}
				if _, reserved := mount.Options["args"]; reserved {
					return fmt.Errorf("mount %s/%s/%s options cannot contain reserved key args", projectID, typeID, mountID)
				}
				for key := range mount.Options {
					if strings.HasPrefix(key, "__ohyeah_") {
						return fmt.Errorf("mount options cannot contain reserved key %s", key)
					}
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
