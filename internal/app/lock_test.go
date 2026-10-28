package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateLockPreventsConcurrentNativeClients(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	raw := fmt.Sprintf("state_path: %q\nmeilisearch:\n  mode: external\nprojects: {}\n", filepath.Join(root, "state.db"))
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err == nil {
		second.Close()
		first.Close()
		t.Fatal("concurrent client acquired state")
	}
	if !strings.Contains(err.Error(), "state is in use") {
		first.Close()
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.Close()
}

func TestStateLockResolvesDatabaseSymlinks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	database := filepath.Join(root, "state.db")
	raw := fmt.Sprintf("state_path: %q\nmeilisearch:\n  mode: external\n", database)
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	alias := filepath.Join(root, "alias.db")
	if err := os.Symlink(database, alias); err != nil {
		t.Skip(err)
	}
	alternate := filepath.Join(root, "alias.yaml")
	if err := os.WriteFile(alternate, []byte(strings.Replace(raw, database, alias, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := Open(alternate)
	if err == nil {
		second.Close()
		t.Fatal("database alias bypassed process lock")
	}
	if !strings.Contains(err.Error(), "state is in use") {
		t.Fatal(err)
	}
}
