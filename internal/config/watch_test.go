package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWatchWaitsForReloadBeforeReturning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("projects: {}"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	go func() {
		done <- Watch(ctx, path, time.Millisecond, func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			<-release
			return ctx.Err()
		})
	}()
	// Retry writes until the watcher is attached; no startup sleep assumption.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	waiting := true
	for waiting {
		select {
		case <-entered:
			waiting = false
		case <-ticker.C:
			if err := os.WriteFile(path, []byte("projects: {}\n"), 0600); err != nil {
				t.Fatal(err)
			}
		case err := <-done:
			t.Fatalf("watcher stopped: %v", err)
		case <-deadline.C:
			t.Fatal("reload did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("returned while reload still active: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not stop")
	}
}
