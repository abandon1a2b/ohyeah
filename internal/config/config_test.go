package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectTypeMountConfiguration(t *testing.T) {
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.yaml")
	content := fmt.Sprintf(`
state_path: %q
meilisearch:
  url: http://127.0.0.1:7700
  index: test
projects:
  example-project:
    workspace: %q
    types:
      markdown:
        collector:
          command: %q
          args: [collect]
          revision: 1
        mounts:
          team-docs:
            root: %q
            revision: 1
            options:
              profile: team
`, filepath.Join(root, "state.db"), root, executable, root)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HasProjectConfig {
		t.Fatal("project configuration should be marked present")
	}
	mounts := cfg.ConfiguredMounts()
	if len(mounts) != 1 || mounts[0].ProjectID != "example-project" || mounts[0].TypeID != "markdown" || mounts[0].MountID != "team-docs" {
		t.Fatalf("mounts = %#v", mounts)
	}
	if !Enabled(mounts[0].Mount.Enabled) {
		t.Fatal("omitted enabled should default to true")
	}
}

func TestEmbeddingConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		invalid        bool
	}{
		{"defaults", "", false},
		{"enabled", "enabled: true", false},
		{"invalid ratio", "enabled: true\n    semantic_ratio: 1.5", true},
		{"zero ratio", "enabled: true\n    semantic_ratio: 0", true},
		{"negative dimensions", "enabled: true\n    dimensions: -1", true},
		{"missing model", "enabled: true\n    model: ''", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte("meilisearch:\n  embedding:\n    "+tc.settings+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if (err != nil) != tc.invalid {
				t.Fatalf("error = %v", err)
			}
			if err == nil && (cfg.Meilisearch.Embedding.Model != "qwen3-embedding:0.6b" || cfg.Meilisearch.Embedding.SemanticRatio != 0.5) {
				t.Fatalf("embedding = %#v", cfg.Meilisearch.Embedding)
			}
		})
	}
}

func TestMeilisearchModeCompatibility(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"", "managed"},
		{"meilisearch:\n  url: http://127.0.0.1:7700\n", "external"},
		{"meilisearch:\n  mode: managed\n  url: http://127.0.0.1:7700\n", "managed"},
		{"meilisearch:\n  mode: external\n", "external"},
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(tc.raw), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Meilisearch.Mode != tc.want {
			t.Fatalf("%q: got %s want %s", tc.raw, cfg.Meilisearch.Mode, tc.want)
		}
	}
}

func TestInvalidManagedConfiguration(t *testing.T) {
	for _, raw := range []string{
		"meilisearch:\n  mode: invalid\n",
		"meilisearch:\n  mode: managed\n  directory: relative/path\n",
		"meilisearch:\n  mode: managed\n  url: http://0.0.0.0:7700\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestMeilisearchEnvironmentOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("meilisearch:\n  url: http://127.0.0.1:7700\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OHYEAH_MEILISEARCH_MODE", "managed")
	t.Setenv("OHYEAH_MEILISEARCH_URL", "http://127.0.0.1:7799")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Meilisearch.Mode != "managed" || cfg.Meilisearch.URL != "http://127.0.0.1:7799" {
		t.Fatalf("overrides: %#v", cfg.Meilisearch)
	}
}
