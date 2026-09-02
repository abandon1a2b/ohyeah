package config

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch calls reload after the active configuration file changes. Watching the
// parent directory keeps it working when editors replace the file atomically.
func Watch(ctx context.Context, path string, debounce time.Duration, reload func(context.Context) error) error {
	if path == "" {
		<-ctx.Done()
		return ctx.Err()
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	if err := watcher.Add(filepath.Dir(path)); err != nil {
		return err
	}
	var timer *time.Timer
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return ctx.Err()
		case err := <-watcher.Errors:
			slog.Warn("configuration watcher error", "error", err)
		case event := <-watcher.Events:
			if filepath.Clean(event.Name) != filepath.Clean(path) || event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(debounce, func() {
				if err := reload(ctx); err != nil {
					slog.Warn("configuration reload rejected; keeping current runtime state", "error", err)
				}
			})
		}
	}
}
