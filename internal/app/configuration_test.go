package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/store"
)

func TestConfigurationConcurrentSaveAndReload(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	raw := fmt.Sprintf("state_path: %q\nserver:\n  address: 127.0.0.1:8787\nprojects: {}\n", filepath.Join(root, "state.db"))
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	service := NewConfiguration(st, cfg)
	ctx := context.Background()
	snapshot, err := service.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.RestartRequired) != 0 {
		t.Fatalf("unchanged configuration requires restart: %v", snapshot.RestartRequired)
	}
	candidate := strings.Replace(raw, "8787", "8788", 1)
	preview, err := service.Validate(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(preview.RestartRequired, []string{"server"}) {
		t.Fatalf("restart fields: %v", preview.RestartRequired)
	}
	current, _ := os.ReadFile(path)
	if string(current) != raw {
		t.Fatal("validation changed file")
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() { <-start; _, err := service.Save(ctx, candidate, snapshot.Hash); results <- err }()
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConfigurationConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	if _, err := service.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := service.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Raw != candidate || !slices.Equal(after.RestartRequired, []string{"server"}) {
		t.Fatalf("saved state: %#v", after)
	}
	if _, err := service.Save(ctx, "projects: [", after.Hash); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("invalid save: %v", err)
	}
	current, _ = os.ReadFile(path)
	if string(current) != candidate {
		t.Fatal("invalid save changed file")
	}
}

func TestConfigurationRegistryReload(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	raw := fmt.Sprintf("state_path: %q\nprojects:\n  example:\n    workspace: %q\n    types: {}\n", filepath.Join(root, "state.db"), root)
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	service := NewConfiguration(st, cfg)
	ctx := context.Background()
	if _, err := service.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	projects, err := st.ListProjects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects=%v error=%v", projects, err)
	}
	snapshot, err := service.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	disabled := strings.Replace(raw, "    types:", "    enabled: false\n    types:", 1)
	result, err := service.Save(ctx, disabled, snapshot.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RestartRequired) != 0 {
		t.Fatal("project changes must apply without restart")
	}
	projects, err = st.ListProjects(ctx)
	if err != nil || projects[0].Enabled {
		t.Fatalf("projects=%v error=%v", projects, err)
	}
}

func TestConfigurationPreservesSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real.yaml")
	alias := filepath.Join(root, "config.yaml")
	raw := fmt.Sprintf("state_path: %q\nprojects: {}\n", filepath.Join(root, "state.db"))
	if err := os.WriteFile(target, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Skip(err)
	}
	cfg, err := config.Load(alias)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	service := NewConfiguration(st, cfg)
	snapshot, err := service.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate := raw + "log_level: debug\n"
	if _, err := service.Save(context.Background(), candidate, snapshot.Hash); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(alias)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("save replaced configuration symlink")
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != candidate {
		t.Fatalf("target not updated: %v", err)
	}
}
