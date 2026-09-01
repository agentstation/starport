package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	urfavecli "github.com/urfave/cli/v3"

	starportcli "github.com/agentstation/starport/internal/cli"
	skill "github.com/agentstation/starport/skills/starport"
)

const (
	agentSetupDirFlag   = "dir"
	agentSetupPrintFlag = "print"
)

// newAgentCommand builds `starport agent`. Its one verb, `starport agent
// setup`, installs the embedded skill into a shared skills root, following
// the pattern `stripe agent setup` set: the CLI carries the skill so the
// installed copy always matches the installed commands.
func newAgentCommand() *urfavecli.Command {
	setup := &urfavecli.Command{
		Name: "setup", Usage: "Install the embedded starport skill into a skills root",
		OnUsageError: subcommandUsageError,
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{
				Name:  agentSetupDirFlag,
				Usage: "Skills root to install into (default: $AGENTS_HOME/skills or ~/.agents/skills)",
			},
			&urfavecli.BoolFlag{
				Name:  agentSetupPrintFlag,
				Usage: "Write the skill to standard output instead of installing it",
			},
		},
		Action: func(_ context.Context, cmd *urfavecli.Command) error {
			if cmd.NArg() > 0 {
				return urfavecli.Exit(
					fmt.Sprintf("%s does not accept arguments", cmd.FullName()),
					starportcli.ExitCodeUsage,
				)
			}
			if cmd.Bool(agentSetupPrintFlag) {
				_, err := fmt.Fprint(cmd.Writer, skill.Markdown)
				return err
			}
			root, err := agentSkillsRoot(cmd.String(agentSetupDirFlag))
			if err != nil {
				return err
			}
			path, err := installAgentSkill(root)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.Writer, "Installed the starport skill: %s\n", path)
			return err
		},
	}
	return &urfavecli.Command{
		Name: "agent", Usage: "Integrate agent harnesses with this gateway",
		OnUsageError: subcommandUsageError,
		Commands:     []*urfavecli.Command{setup},
		Action: func(_ context.Context, cmd *urfavecli.Command) error {
			_ = urfavecli.ShowSubcommandHelp(cmd)
			if cmd.NArg() > 0 {
				return urfavecli.Exit(
					fmt.Sprintf("unknown agent command %q", cmd.Args().First()),
					starportcli.ExitCodeUsage,
				)
			}
			return nil
		},
	}
}

// agentSkillsRoot resolves the shared skills root the agentskills convention
// names: an explicit flag wins, then $AGENTS_HOME/skills, then
// ~/.agents/skills.
func agentSkillsRoot(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if agentsHome := os.Getenv("AGENTS_HOME"); agentsHome != "" {
		return filepath.Join(agentsHome, "skills"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve the home directory: %w", err)
	}
	return filepath.Join(home, ".agents", "skills"), nil
}

// installAgentSkill writes the embedded SKILL.md under the skills root,
// replacing any older copy so the skill tracks the installed CLI.
func installAgentSkill(root string) (string, error) {
	directory := filepath.Join(root, skill.Name)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", fmt.Errorf("create the skill directory: %w", err)
	}
	path := filepath.Join(directory, "SKILL.md")
	if err := os.WriteFile(path, []byte(skill.Markdown), 0o600); err != nil {
		return "", fmt.Errorf("write the skill: %w", err)
	}
	return path, nil
}
