package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	starportcli "github.com/agentstation/starport/internal/cli"
	skill "github.com/agentstation/starport/skills/starport"
)

func runAgentCommand(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := runContext(
		context.Background(),
		args,
		bytes.NewReader(nil),
		stdout,
		stderr,
		func(context.Context, starportcli.GatewayOptions) error { return nil },
		noopDevelopmentStarter,
		noopInitializer,
	)
	return stdout.String(), stderr.String(), code
}

func TestAgentSetupWritesTheEmbeddedSkill(t *testing.T) {
	root := t.TempDir()
	stdout, stderr, code := runAgentCommand(t, "starport", "agent", "setup", "--dir", root)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
	}
	path := filepath.Join(root, "starport", "SKILL.md")
	if !strings.Contains(stdout, path) {
		t.Errorf("output = %q, want the installed path %q", stdout, path)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read installed skill: %v", err)
	}
	if string(written) != skill.Markdown {
		t.Error("installed skill differs from the embedded skill")
	}
	if !strings.Contains(skill.Markdown, "name: starport") {
		t.Error("the embedded skill names no skill")
	}
}

func TestAgentSetupOverwritesAnOlderSkill(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "starport")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(directory, "SKILL.md")
	if err := os.WriteFile(stale, []byte("stale skill from an older CLI"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runAgentCommand(t, "starport", "agent", "setup", "--dir", root)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
	}
	written, err := os.ReadFile(stale)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != skill.Markdown {
		t.Error("setup left the stale skill in place")
	}
}

func TestAgentSetupDefaultsToTheAgentsHome(t *testing.T) {
	agentsHome := t.TempDir()
	t.Setenv("AGENTS_HOME", agentsHome)
	stdout, stderr, code := runAgentCommand(t, "starport", "agent", "setup")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
	}
	path := filepath.Join(agentsHome, "skills", "starport", "SKILL.md")
	if !strings.Contains(stdout, path) {
		t.Errorf("output = %q, want the installed path %q", stdout, path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("installed skill: %v", err)
	}
}

func TestAgentSetupPrintsTheSkill(t *testing.T) {
	agentsHome := t.TempDir()
	t.Setenv("AGENTS_HOME", agentsHome)
	stdout, stderr, code := runAgentCommand(t, "starport", "agent", "setup", "--print")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if stdout != skill.Markdown {
		t.Error("printed skill differs from the embedded skill")
	}
	if _, err := os.Stat(filepath.Join(agentsHome, "skills")); !os.IsNotExist(err) {
		t.Errorf("print mode wrote to the skills root: %v", err)
	}
}

func TestAgentSetupRejectsArguments(t *testing.T) {
	_, stderr, code := runAgentCommand(t, "starport", "agent", "setup", "extra")
	if code != starportcli.ExitCodeUsage {
		t.Errorf("exit code = %d, want %d; stderr = %s", code, starportcli.ExitCodeUsage, stderr)
	}
}
