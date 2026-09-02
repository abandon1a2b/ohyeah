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
