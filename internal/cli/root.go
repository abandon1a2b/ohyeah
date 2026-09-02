package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/app"
	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/abandon1a2b/ohyeah/internal/model"
	"github.com/abandon1a2b/ohyeah/internal/registry"
	"github.com/abandon1a2b/ohyeah/internal/source"
	"github.com/abandon1a2b/ohyeah/internal/syncer"
	"github.com/abandon1a2b/ohyeah/internal/web"
	"github.com/spf13/cobra"
)

const version = "0.1.0-dev"

type options struct {
	configPath string
	jsonOutput bool
	out        io.Writer
}

func Execute() error {
	opts := &options{out: os.Stdout}
	root := newRootCommand(opts)
	return root.Execute()
}

func newRootCommand(opts *options) *cobra.Command {
	root := &cobra.Command{
		Use:           "ohyeah",
		Short:         "Retrieve prior work context with provenance",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&opts.configPath, "config", "", "configuration file path")
	root.PersistentFlags().BoolVar(&opts.jsonOutput, "json", false, "emit machine-readable JSON")
	root.AddCommand(
		newInitCommand(opts),
		newConfigCommand(opts),
		newSourceCommand(opts),
		newSyncCommand(opts),
		newIndexCommand(opts),
		newSearchCommand(opts),
		newGetCommand(opts),
		newStatusCommand(opts),
		newServeCommand(opts),
		&cobra.Command{Use: "version", Short: "Print version", RunE: func(cmd *cobra.Command, args []string) error {
			return writeOutput(opts, map[string]string{"version": version}, version)
		}},
	)
	return root
}

func newIndexCommand(opts *options) *cobra.Command {
	command := &cobra.Command{Use: "index", Short: "Inspect and rebuild the search index"}
	doctor := &cobra.Command{
		Use: "doctor", Short: "Compare SQLite memory with the search index", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(opts)
			if err != nil {
				return err
			}
			defer application.Close()
			result, err := application.Syncer.Doctor(cmd.Context())
			if err != nil {
				return err
			}
			if err := writeOutput(opts, result, fmt.Sprintf("sqlite=%d meilisearch=%d indexExists=%v consistent=%v", result.SQLiteCount, result.MeilisearchCount, result.IndexExists, result.Consistent)); err != nil {
				return err
			}
			if !result.Consistent {
				return errors.New("search index is inconsistent; run ohyeah index rebuild")
			}
			return nil
		},
	}
	rebuild := &cobra.Command{
		Use: "rebuild", Short: "Rebuild the search index entirely from SQLite", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(opts)
			if err != nil {
				return err
			}
			defer application.Close()
			count, err := application.Syncer.RebuildIndex(cmd.Context())
			if err != nil {
				return err
			}
			return writeOutput(opts, map[string]any{"rebuilt": true, "documents": count}, fmt.Sprintf("rebuilt %d documents", count))
		},
	}
	command.AddCommand(doctor, rebuild)
	return command
}

func newConfigCommand(opts *options) *cobra.Command {
	command := &cobra.Command{Use: "config", Short: "Validate and apply project, type, and mount configuration"}
	validate := &cobra.Command{
		Use: "validate", Short: "Validate configured projects, types, mounts, and collectors", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(opts.configPath)
			if err != nil {
				return err
			}
			mounts := cfg.ConfiguredMounts()
			return writeOutput(opts, map[string]any{"valid": true, "projects": len(cfg.Projects), "mounts": len(mounts)}, fmt.Sprintf("valid: %d projects, %d mounts", len(cfg.Projects), len(mounts)))
		},
	}
	plan := &cobra.Command{
		Use: "plan", Short: "Show configuration changes without applying them", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(opts)
			if err != nil {
				return err
			}
			defer application.Close()
			actions, err := registry.New(application.Store, application.Config).Plan(cmd.Context())
			if err != nil {
				return err
			}
			return writeActions(opts, actions)
		},
	}
	apply := &cobra.Command{
		Use: "apply", Short: "Apply configured projects, types, and mounts to runtime state", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(opts)
			if err != nil {
				return err
			}
			defer application.Close()
			actions, err := registry.New(application.Store, application.Config).Apply(cmd.Context())
			if err != nil {
				if opts.jsonOutput {
					_ = writeOutput(opts, actions, "")
				}
				return err
			}
			return writeActions(opts, actions)
		},
	}
	tree := &cobra.Command{
		Use: "tree", Short: "Show the configured project, type, and mount hierarchy", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(opts.configPath)
			if err != nil {
				return err
			}
			if opts.jsonOutput {
				return writeOutput(opts, cfg.Projects, "")
			}
			return writeConfigTree(opts.out, cfg)
		},
	}
	command.AddCommand(validate, tree, plan, apply)
	return command
}

func writeConfigTree(out io.Writer, cfg config.Config) error {
	projectIDs := make([]string, 0, len(cfg.Projects))
	for projectID := range cfg.Projects {
		projectIDs = append(projectIDs, projectID)
	}
	slices.Sort(projectIDs)
	for _, projectID := range projectIDs {
		project := cfg.Projects[projectID]
		projectEnabled := config.Enabled(project.Enabled)
		fmt.Fprintf(out, "%s\t[%s]\t%s\n", projectID, enabledLabel(projectEnabled), project.Workspace)
		typeIDs := make([]string, 0, len(project.Types))
		for typeID := range project.Types {
			typeIDs = append(typeIDs, typeID)
		}
		slices.Sort(typeIDs)
		for _, typeID := range typeIDs {
			dataType := project.Types[typeID]
			typeEnabled := projectEnabled && config.Enabled(dataType.Enabled)
			fmt.Fprintf(out, "  %s\t[%s]\tcollector=%s\trevision=%d\n", typeID, enabledLabel(typeEnabled), dataType.Collector.Command, dataType.Collector.Revision)
			mountIDs := make([]string, 0, len(dataType.Mounts))
			for mountID := range dataType.Mounts {
				mountIDs = append(mountIDs, mountID)
			}
			slices.Sort(mountIDs)
			for _, mountID := range mountIDs {
				mount := dataType.Mounts[mountID]
				mountEnabled := typeEnabled && config.Enabled(mount.Enabled)
				fmt.Fprintf(out, "    %s\t[%s]\troot=%s\trevision=%d\n", mountID, enabledLabel(mountEnabled), mount.Root, mount.Revision)
			}
		}
	}
	return nil
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func newInitCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize local state and the search index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(opts)
			if err != nil {
				return err
			}
			defer application.Close()
			if err := application.Ensure(cmd.Context()); err != nil {
				return err
			}
			return writeOutput(opts, map[string]any{"initialized": true, "statePath": application.Config.StatePath, "index": application.Config.Meilisearch.Index}, "initialized")
		},
	}
}

func newSourceCommand(opts *options) *cobra.Command {
	command := &cobra.Command{Use: "source", Short: "Manage indexed data sources"}
	var id, workspace, path, collectorCommand string
	var collectorArgs, collectorOptions []string
	add := &cobra.Command{
		Use:   "add",
		Short: "Register or update a source",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			absolute, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			info, err := os.Stat(absolute)
			if err != nil {
				return err
			}
			if !info.IsDir() {
				return fmt.Errorf("source path is not a directory: %s", absolute)
			}
			application, err := openApp(opts)
			if err != nil {
				return err
			}
			defer application.Close()
			now := time.Now().UTC()
			options, err := parseOptions(collectorOptions)
			if err != nil {
				return err
			}
			options["command"] = collectorCommand
			options["args"] = collectorArgs
			registered := model.Source{
				ID: id, TypeID: "external", MountName: id, WorkspaceID: workspace, Driver: model.DriverCommand, Path: absolute,
				Enabled: true, ManagedBy: model.ManagedByCLI, State: model.SourceStateActive,
				Options: options, CreatedAt: now, UpdatedAt: now,
			}
			if _, err := source.New(registered); err != nil {
				return err
			}
			if err := application.Store.UpsertSource(cmd.Context(), registered); err != nil {
				return err
			}
			return writeOutput(opts, registered, "source registered: "+id)
		},
	}
	add.Flags().StringVar(&id, "id", "", "stable source identifier")
	add.Flags().StringVar(&workspace, "workspace", "", "workspace identifier")
	add.Flags().StringVar(&path, "path", "", "source directory")
	add.Flags().StringVar(&collectorCommand, "collector", "", "collector executable for command sources")
	add.Flags().StringArrayVar(&collectorArgs, "collector-arg", nil, "collector argument; repeat for multiple arguments")
	add.Flags().StringArrayVar(&collectorOptions, "option", nil, "collector option as key=value; repeat for multiple options")
	_ = add.MarkFlagRequired("id")
	_ = add.MarkFlagRequired("workspace")
	_ = add.MarkFlagRequired("path")
	_ = add.MarkFlagRequired("collector")

	list := &cobra.Command{
		Use:   "list",
		Short: "List registered sources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(opts)
			if err != nil {
				return err
			}
			defer application.Close()
			sources, err := application.Store.ListSources(cmd.Context(), false)
			if err != nil {
				return err
			}
			if opts.jsonOutput {
				return writeOutput(opts, sources, "")
			}
			for _, registered := range sources {
				fmt.Fprintf(opts.out, "%s\t%s\t%s\t%s\n", registered.ID, registered.WorkspaceID, registered.Driver, registered.Path)
			}
			return nil
		},
	}

	remove := &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove a source and enqueue deletion of its memory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(opts)
			if err != nil {
				return err
			}
			defer application.Close()
			registered, err := application.Store.GetSource(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if registered.ManagedBy == model.ManagedByConfig {
				return errors.New("mount is managed by configuration; remove it from config and run config apply")
			}
			if err := application.Store.RemoveSource(cmd.Context(), args[0]); err != nil {
				return err
			}
			if err := application.Syncer.Dispatch(cmd.Context()); err != nil {
				return err
			}
			return writeOutput(opts, map[string]any{"removed": args[0]}, "source removed: "+args[0])
		},
	}
	command.AddCommand(add, list, remove)
	return command
}

func newSyncCommand(opts *options) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "sync [source-id]",
		Short: "Synchronize one source or all enabled sources",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(opts)
			if err != nil {
				return err
			}
			defer application.Close()
			if dryRun {
				if len(args) == 1 {
					preview, err := application.Syncer.PreviewByID(cmd.Context(), args[0])
					if err != nil {
						return err
					}
					return writeOutput(opts, preview, fmt.Sprintf("%s: documents=%d units=%d change=%d delete=%d", preview.SourceID, preview.Documents, preview.MemoryUnits, preview.WouldChange, preview.WouldDelete))
				}
				previews, err := application.Syncer.PreviewAll(cmd.Context())
				if err != nil && !opts.jsonOutput {
					for _, preview := range previews {
						fmt.Fprintf(opts.out, "%s\tdocuments=%d units=%d change=%d delete=%d error=%s\n", preview.SourceID, preview.Documents, preview.MemoryUnits, preview.WouldChange, preview.WouldDelete, preview.Error)
					}
					return err
				}
				if outputErr := writeOutput(opts, previews, fmt.Sprintf("previewed %d mounts", len(previews))); outputErr != nil {
					return outputErr
				}
				return err
			}
			if len(args) == 1 {
				run, err := application.Syncer.SyncByID(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return writeOutput(opts, run, fmt.Sprintf("%s: scanned=%d changed=%d deleted=%d", run.SourceID, run.Scanned, run.Changed, run.Deleted))
			}
			runs, err := application.Syncer.SyncAll(cmd.Context())
			if err != nil {
				if opts.jsonOutput {
					_ = writeOutput(opts, runs, "")
				} else {
					for _, run := range runs {
						fmt.Fprintf(opts.out, "%s\t%s\tscanned=%d changed=%d deleted=%d\n", run.SourceID, run.Status, run.Scanned, run.Changed, run.Deleted)
					}
				}
				return err
			}
			return writeOutput(opts, runs, fmt.Sprintf("synchronized %d sources", len(runs)))
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "run collectors and preview changes without modifying state or index")
	return command
}

func newSearchCommand(opts *options) *cobra.Command {
	var workspace, projectID, typeID, mountID string
	var kinds []string
	var limit int
	command := &cobra.Command{
		Use:   "search <query>",
		Short: "Search prior work memory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(projectID) == "" {
				return errors.New("search requires --project")
			}
			application, err := openApp(opts)
			if err != nil {
				return err
			}
			defer application.Close()
			hits, err := application.Syncer.Search(cmd.Context(), model.SearchQuery{
				Query: args[0], ProjectID: projectID, TypeID: typeID, MountID: mountID,
				WorkspaceID: workspace, Kinds: kinds, Limit: limit,
			})
			if err != nil {
				return err
			}
			if opts.jsonOutput {
				return writeOutput(opts, hits, "")
			}
			for _, hit := range hits {
				where := hit.Unit.Path
				if hit.Unit.ThreadID != "" {
					where = "thread:" + hit.Unit.ThreadID
				}
				fmt.Fprintf(opts.out, "%s\t%s\t%s\t%s\n", hit.Unit.ID, hit.Unit.Kind, where, hit.Unit.Title)
			}
			return nil
		},
	}
	command.Flags().StringVar(&workspace, "workspace", "", "limit results to a workspace")
	command.Flags().StringVar(&projectID, "project", "", "project to search (required)")
	_ = command.MarkFlagRequired("project")
	command.Flags().StringVar(&typeID, "type", "", "limit results to a data type")
	command.Flags().StringVar(&mountID, "mount", "", "limit results to a canonical mount id")
	command.Flags().StringSliceVar(&kinds, "kind", nil, "limit results to one or more memory kinds")
	command.Flags().IntVar(&limit, "limit", 10, "maximum results")
	return command
}

func newGetCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "get <memory-id>",
		Short: "Retrieve a memory unit and its provenance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(opts)
			if err != nil {
				return err
			}
			defer application.Close()
			record, err := application.Store.GetMemory(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if opts.jsonOutput {
				return writeOutput(opts, record, "")
			}
			fmt.Fprintf(opts.out, "%s\n\n%s\n\nsource: %s\n", record.Unit.Title, record.Unit.Content, provenance(record))
			return nil
		},
	}
}

func newStatusCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show synchronization and backend status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(opts)
			if err != nil {
				return err
			}
			defer application.Close()
			stats, err := application.Store.Stats(cmd.Context())
			if err != nil {
				return err
			}
			backendStatus := "available"
			if err := application.Backend.Healthy(cmd.Context()); err != nil {
				backendStatus = err.Error()
			}
			result := map[string]any{"statePath": application.Config.StatePath, "index": application.Config.Meilisearch.Index, "backend": backendStatus, "stats": stats}
			if opts.jsonOutput {
				return writeOutput(opts, result, "")
			}
			fmt.Fprintf(opts.out, "backend: %s\nindex: %s\n", backendStatus, application.Config.Meilisearch.Index)
			for _, key := range []string{"sources", "objects", "memoryUnits", "relations", "pendingOutbox", "failedOutbox"} {
				fmt.Fprintf(opts.out, "%s: %d\n", key, stats[key])
			}
			return nil
		},
	}
}

func newServeCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run background synchronization",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			application, err := openApp(opts)
			if err != nil {
				return err
			}
			defer application.Close()
			if _, err := registry.New(application.Store, application.Config).Apply(cmd.Context()); err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			sources, err := application.Store.ListSources(ctx, true)
			if err != nil {
				return err
			}
			errCh := make(chan error, 4)
			go func() { errCh <- application.Syncer.RunPeriodic(ctx) }()
			go func() {
				errCh <- syncer.WatchDynamic(ctx, func(listCtx context.Context) ([]model.Source, error) {
					return application.Store.ListSources(listCtx, true)
				}, application.Config.Sync.Debounce, func(syncCtx context.Context) error {
					_, err := application.Syncer.SyncAll(syncCtx)
					return err
				})
			}()
			go func() {
				errCh <- config.Watch(ctx, application.Config.Path, application.Config.Sync.Debounce, func(reloadCtx context.Context) error {
					updated, err := config.Load(application.Config.Path)
					if err != nil {
						return err
					}
					_, err = registry.New(application.Store, updated).Apply(reloadCtx)
					return err
				})
			}()
			go func() { errCh <- web.Run(ctx, application, application.Config.Server.Address) }()
			slog.Info("ohyeah service started", "sources", len(sources), "web", "http://"+application.Config.Server.Address)
			err = <-errCh
			if err != nil && err != context.Canceled {
				return err
			}
			return nil
		},
	}
}

func writeActions(opts *options, actions []registry.Action) error {
	if opts.jsonOutput {
		return writeOutput(opts, actions, "")
	}
	if len(actions) == 0 {
		_, err := fmt.Fprintln(opts.out, "no configured source changes")
		return err
	}
	for _, action := range actions {
		cursor := ""
		if action.ResetCursor {
			cursor = " reset-cursor"
		}
		fmt.Fprintf(opts.out, "%s\t%s\t%s\t%s%s\n", action.Entity, action.Type, action.ID, action.Reason, cursor)
	}
	return nil
}

func openApp(opts *options) (*app.App, error) {
	return app.Open(opts.configPath)
}

func writeOutput(opts *options, value any, human string) error {
	if opts.jsonOutput {
		encoder := json.NewEncoder(opts.out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	if human != "" {
		_, err := fmt.Fprintln(opts.out, human)
		return err
	}
	return nil
}

func provenance(record model.MemoryRecord) string {
	unit := record.Unit
	parts := []string{unit.SourceType, unit.SourceID}
	if unit.ThreadID != "" {
		parts = append(parts, "thread="+unit.ThreadID)
	}
	if unit.TurnID != "" {
		parts = append(parts, "turn="+unit.TurnID)
	}
	if unit.Path != "" {
		parts = append(parts, "path="+unit.Path)
	}
	for _, key := range []string{"path", "url", "aliases"} {
		if value := record.Reference[key]; value != "" && (key != "path" || value != unit.Path) {
			parts = append(parts, key+"="+value)
		}
	}
	return strings.Join(parts, " ")
}

func parseOptions(values []string) (map[string]any, error) {
	result := make(map[string]any, len(values))
	for _, value := range values {
		key, item, found := strings.Cut(value, "=")
		if !found || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("collector option must use key=value: %q", value)
		}
		result[strings.TrimSpace(key)] = item
	}
	return result, nil
}
