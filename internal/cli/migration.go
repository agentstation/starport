package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	urfavecli "github.com/urfave/cli/v3"
)

// RuntimeMigrator executes one durable phase without starting the gateway.
type RuntimeMigrator func(context.Context, *config.Config, string, catalog.RuntimeMigration) (catalog.RuntimeMigrationResult, error)

func newMigrationCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	runtime := &urfavecli.Command{Name: "runtime", Usage: "Move stopped catalog runtime state while retaining the catalog database"}
	for _, phase := range []string{"prepare", "stage", "publish", "complete"} {
		runtime.Commands = append(runtime.Commands, &urfavecli.Command{
			Name: phase, Usage: "Run the " + phase + " migration phase", OnUsageError: usageError,
			Flags: []urfavecli.Flag{
				&urfavecli.StringFlag{Name: "operation", Required: true, Usage: "Stable operation identifier for all phases"},
				&urfavecli.StringFlag{Name: "source", Required: true, Usage: "Absolute original runtime directory"},
				&urfavecli.StringFlag{Name: "target", Required: true, Usage: "Absolute replacement runtime directory"},
				&urfavecli.StringFlag{Name: "journal", Required: true, Usage: "Absolute private migration journal directory"},
				&urfavecli.StringFlag{Name: "identity", Required: true, Usage: "Existing scheduler identity to retain"},
				&urfavecli.BoolFlag{Name: "json", Usage: jsonOutputUsage},
			},
			Action: func(ctx context.Context, cmd *urfavecli.Command) error {
				if err := rejectArguments(cmd); err != nil {
					return err
				}
				for _, flag := range []string{"source", "target", "journal"} {
					if !filepath.IsAbs(cmd.String(flag)) {
						return urfavecli.Exit("--"+flag+" requires an absolute path", ExitCodeUsage)
					}
				}
				if deps.MigrateRuntime == nil {
					return runtimeFailure{cause: fmt.Errorf("runtime migration runner is required")}
				}
				cfg, err := deps.LoadConfig(ctx)
				if err != nil {
					return runtimeFailure{cause: err}
				}
				request := catalog.RuntimeMigration{OperationID: cmd.String("operation"), SourceDirectory: filepath.Clean(cmd.String("source")), TargetDirectory: filepath.Clean(cmd.String("target")), JournalRoot: filepath.Clean(cmd.String("journal")), SourceIdentity: cmd.String("identity")}
				result, err := deps.MigrateRuntime(ctx, cfg, phase, request)
				if err != nil {
					return runtimeFailure{cause: err}
				}
				if cmd.Bool("json") {
					return writeIndentedJSON(cmd.Writer, result)
				}
				_, err = fmt.Fprintf(cmd.Writer, "Phase: %s\nTarget: %s\nJournal: %s\nHost journal: %s\nScheduler identity: %s\n", result.Phase, result.TargetDirectory, result.JournalDirectory, result.HostJournalDirectory, result.SchedulerIdentity)
				return err
			},
		})
	}
	return &urfavecli.Command{Name: "migrate", Usage: "Run an explicit storage migration", Commands: []*urfavecli.Command{runtime}}
}
