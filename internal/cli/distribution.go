package cli

import (
	"context"
	"fmt"
	"io"

	urfaveclidocs "github.com/urfave/cli-docs/v3"
	urfavecli "github.com/urfave/cli/v3"
)

func configureCompletionCommand(
	descriptor *urfavecli.Command,
	usageError usageErrorHandler,
) urfavecli.ConfigureShellCompletionCommand {
	return func(command *urfavecli.Command) {
		command.Name = descriptor.Name
		command.Usage = descriptor.Usage
		command.Hidden = false
		command.OnUsageError = usageError
		for _, shell := range command.Commands {
			shell.OnUsageError = usageError
			action := shell.Action
			shell.Action = func(ctx context.Context, command *urfavecli.Command) error {
				if err := action(ctx, command); err != nil {
					return runtimeFailure{cause: fmt.Errorf("generate completion: %w", err)}
				}
				return nil
			}
		}
	}
}

func writeManPage(command *urfavecli.Command, output io.Writer) error {
	manual, err := urfaveclidocs.ToManWithSection(command, 1)
	if err != nil {
		return fmt.Errorf("generate manual page: %w", err)
	}
	if _, err := io.WriteString(output, manual); err != nil {
		return fmt.Errorf("write manual page: %w", err)
	}
	return nil
}
