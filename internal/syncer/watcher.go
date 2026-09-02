package syncer

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/fsnotify/fsnotify"
)

func Watch(ctx context.Context, sources []model.Source, debounce time.Duration, syncAll func(context.Context) error) error {
	return WatchDynamic(ctx, func(context.Context) ([]model.Source, error) { return sources, nil }, debounce, syncAll)
}

func WatchDynamic(ctx context.Context, listSources func(context.Context) ([]model.Source, error), debounce time.Duration, syncAll func(context.Context) error) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	var sources []model.Source
	addedRoots := make(map[string]struct{})
	refresh := func() {
		current, listErr := listSources(ctx)
		if listErr != nil {
			slog.Warn("list watch sources failed", "error", listErr)
			return
		}
		sources = current
		for _, source := range sources {
			if !watchEnabled(source) {
				continue
			}
			root := filepath.Clean(source.Path)
			if _, exists := addedRoots[root]; exists {
				continue
			}
			if err := addTree(watcher, root); err != nil {
				slog.Warn("watch source failed", "source", source.ID, "error", err)
				continue
			}
			addedRoots[root] = struct{}{}
		}
	}
	refresh()
	refreshTicker := time.NewTicker(2 * time.Second)
	defer refreshTicker.Stop()
	trigger := make(chan struct{}, 1)
	go func() {
		var timer *time.Timer
		for {
			select {
			case <-ctx.Done():
				if timer != nil {
					timer.Stop()
				}
				return
			case <-trigger:
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(debounce, func() {
					if err := syncAll(ctx); err != nil {
						slog.Warn("watch sync failed", "error", err)
					}
				})
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-refreshTicker.C:
			refresh()
		case err := <-watcher.Errors:
			slog.Warn("filesystem watcher error", "error", err)
		case event := <-watcher.Events:
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					_ = addTree(watcher, event.Name)
				}
			}
			if relevantFile(event.Name, sources) {
				select {
				case trigger <- struct{}{}:
				default:
				}
			}
		}
	}
}

func addTree(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root && strings.HasPrefix(entry.Name(), ".") {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

func relevantFile(path string, sources []model.Source) bool {
	extension := strings.ToLower(filepath.Ext(path))
	for _, source := range sources {
		if !watchEnabled(source) || !withinRoot(path, source.Path) {
			continue
		}
		configured, _ := source.Options["watch_extensions"].(string)
		if strings.TrimSpace(configured) == "" {
			return true
		}
		for _, candidate := range strings.Split(configured, ",") {
			candidate = strings.TrimSpace(strings.ToLower(candidate))
			if candidate != "" && !strings.HasPrefix(candidate, ".") {
				candidate = "." + candidate
			}
			if extension == candidate {
				return true
			}
		}
	}
	return false
}

func watchEnabled(source model.Source) bool {
	switch value := source.Options["watch"].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(value, "true") || value == "1"
	case int:
		return value == 1
	default:
		return false
	}
}

func withinRoot(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
