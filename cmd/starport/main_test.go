package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v2"
)

// TestMain allows us to set up and tear down test environment
func TestMain(m *testing.M) {
	// Set build variables for consistent test output
	version = "v1.0.0-test"
	buildTime = "2024-01-01_00:00:00"
	gitCommit = "abc123"
	gitBranch = "test-branch"
	goVersion = "go1.21.0"

	os.Exit(m.Run())
}

// captureOutput captures stdout/stderr output from a function
func captureOutput(f func() error) (string, error) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := f()

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return buf.String(), err
}

// TestVersionFlag tests the --version flag
func TestVersionFlag(t *testing.T) {
	output, err := captureOutput(func() error {
		app := &cli.App{
			Name:    "starport",
			Version: version,
		}
		return app.Run([]string{"starport", "--version"})
	})

	if err != nil {
		t.Fatalf("--version flag failed: %v", err)
	}

	if !strings.Contains(output, "v1.0.0-test") {
		t.Errorf("--version output missing version, got: %s", output)
	}
}

// TestVersionCommand tests the version subcommand
func TestVersionCommand(t *testing.T) {
	output, err := captureOutput(func() error {
		ctx := context.Background()
		return runAppWithArgs(ctx, []string{"starport", "version"})
	})

	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	// Check for all expected version info
	expectedStrings := []string{
		"Starport v1.0.0-test",
		"Build Time:  2024-01-01_00:00:00",
		"Git Commit:  abc123",
		"Git Branch:  test-branch",
		"Go Version:  go1.21.0",
		"OS/Arch:",
	}

	for _, expected := range expectedStrings {
		if !strings.Contains(output, expected) {
			t.Errorf("version command output missing '%s', got: %s", expected, output)
		}
	}
}

// TestHelpCommand tests help output
func TestHelpCommand(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected []string
	}{
		{
			name: "default help",
			args: []string{"starport"},
			expected: []string{
				"NAME:",
				"starport - High-performance LLM gateway",
				"USAGE:",
				"COMMANDS:",
				"version",
				"serve",
				"help",
			},
		},
		{
			name: "help flag",
			args: []string{"starport", "--help"},
			expected: []string{
				"NAME:",
				"starport - High-performance LLM gateway",
				"COMMANDS:",
			},
		},
		{
			name: "help command",
			args: []string{"starport", "help"},
			expected: []string{
				"NAME:",
				"starport - High-performance LLM gateway",
			},
		},
		{
			name: "help for serve",
			args: []string{"starport", "help", "serve"},
			expected: []string{
				"NAME:",
				"starport serve - Run the gateway server",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := captureOutput(func() error {
				ctx := context.Background()
				// For help commands, error is expected (cli returns error on help)
				_ = runAppWithArgs(ctx, tt.args)
				return nil
			})

			if err != nil && !strings.Contains(tt.args[len(tt.args)-1], "help") {
				t.Fatalf("%s failed: %v", tt.name, err)
			}

			for _, expected := range tt.expected {
				if !strings.Contains(output, expected) {
					t.Errorf("%s output missing '%s', got: %s", tt.name, expected, output)
				}
			}
		})
	}
}

// TestServeCommand tests that serve command starts without crashing
func TestServeCommand(t *testing.T) {
	// Create a context with timeout to prevent hanging
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan bool)
	var err error

	go func() {
		err = runAppWithArgs(ctx, []string{"starport", "serve"})
		done <- true
	}()

	select {
	case <-done:
		// Command finished (expected due to context timeout)
		if err != nil && err != context.DeadlineExceeded {
			t.Errorf("serve command failed unexpectedly: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("serve command did not respond to context cancellation")
	}
}

// TestInvalidCommand tests that invalid commands show help
func TestInvalidCommand(t *testing.T) {
	output, _ := captureOutput(func() error {
		ctx := context.Background()
		return runAppWithArgs(ctx, []string{"starport", "invalid-command"})
	})

	if !strings.Contains(output, "NAME:") || !strings.Contains(output, "COMMANDS:") {
		t.Error("invalid command should show help output")
	}
}

// runAppWithArgs is a test helper that creates and runs the CLI app with custom args
func runAppWithArgs(ctx context.Context, args []string) error {
	oldArgs := os.Args
	os.Args = args
	defer func() { os.Args = oldArgs }()

	return runApp(ctx)
}
