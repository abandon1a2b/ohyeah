package cli

import (
	"fmt"
	"github.com/abandon1a2b/ohyeah/internal/store"
	"github.com/spf13/cobra"
)

func newStateCommand(opts *options) *cobra.Command {
	command := &cobra.Command{Use: "state", Short: "Offline SQLite backup, integrity, and retention maintenance"}
	command.AddCommand(&cobra.Command{Use: "backup <new-file>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		application, err := openApp(opts)
		if err != nil {
			return err
		}
		defer application.Close()
		if err := application.Store.Backup(cmd.Context(), args[0]); err != nil {
			return err
		}
		return writeOutput(opts, map[string]any{"backup": args[0], "verified": true}, "backup verified: "+args[0])
	}})
	command.AddCommand(&cobra.Command{Use: "check", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		application, err := openApp(opts)
		if err != nil {
			return err
		}
		defer application.Close()
		if err := application.Store.Check(cmd.Context()); err != nil {
			return err
		}
		return writeOutput(opts, map[string]bool{"valid": true}, "state integrity verified")
	}})
	dryRun := true
	prune := &cobra.Command{Use: "prune", Short: "Preview retention cleanup; use --dry-run=false to apply", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		application, err := openApp(opts)
		if err != nil {
			return err
		}
		defer application.Close()
		cfg := application.Config.Maintenance
		result, err := application.Store.Prune(cmd.Context(), store.Retention{Outbox: cfg.OutboxRetention, SyncRuns: cfg.SyncRetention, Audit: cfg.AuditRetention}, dryRun)
		if err != nil {
			return err
		}
		return writeOutput(opts, result, fmt.Sprintf("outbox=%d syncRuns=%d audit=%d dryRun=%v", result.Outbox, result.SyncRuns, result.Audit, dryRun))
	}}
	prune.Flags().BoolVar(&dryRun, "dry-run", true, "preview without deleting")
	command.AddCommand(prune)
	return command
}
