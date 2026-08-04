package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
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
				"starport serve - Run the llm gateway server",
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
	runServerCommandTest(t, []string{"starport", "serve"})
}

func TestDefaultCommandStartsServer(t *testing.T) {
	runServerCommandTest(t, []string{"starport"})
}

func runServerCommandTest(t *testing.T, args []string) {
	t.Helper()

	// Skip this test if we're in a CI environment where network calls might hang
	if os.Getenv("CI") == "true" {
		t.Skip("Skipping server command test in CI environment")
	}

	// Find an available port to avoid conflicts
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to find available port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Make sure port is actually free by waiting a bit
	time.Sleep(50 * time.Millisecond)

	// Use the available port for testing
	oldPort := os.Getenv("STARPORT_SERVER_PORT")
	oldBadgerPath := os.Getenv("STARPORT_STORAGE_BADGER_PATH")
	oldMasterKey := os.Getenv("STARPORT_SECURITY_MASTER_KEY")
	oldBootstrapKey := os.Getenv("STARPORT_SECURITY_BOOTSTRAP_API_KEY")
	oldProviderKey := os.Getenv("STARPORT_PROVIDERS_OPENAI_API_KEY")
	os.Setenv("STARPORT_SERVER_PORT", fmt.Sprintf("%d", port))
	os.Setenv("STARPORT_STORAGE_BADGER_PATH", t.TempDir())
	os.Setenv("STARPORT_SECURITY_MASTER_KEY", strings.Repeat("k", 32))
	os.Setenv("STARPORT_SECURITY_BOOTSTRAP_API_KEY", strings.Repeat("b", 32))
	os.Setenv("STARPORT_PROVIDERS_OPENAI_API_KEY", "test-provider-key")
	t.Logf("Using port %d for test (STARPORT_SERVER_PORT=%s)", port, os.Getenv("STARPORT_SERVER_PORT"))

	// Also check if port 8080 is in use
	if l, err := net.Listen("tcp", ":8080"); err != nil {
		t.Logf("WARNING: Port 8080 is already in use: %v", err)
	} else {
		l.Close()
	}

	defer func() {
		if oldPort != "" {
			os.Setenv("STARPORT_SERVER_PORT", oldPort)
		} else {
			os.Unsetenv("STARPORT_SERVER_PORT")
		}
		if oldBadgerPath != "" {
			os.Setenv("STARPORT_STORAGE_BADGER_PATH", oldBadgerPath)
		} else {
			os.Unsetenv("STARPORT_STORAGE_BADGER_PATH")
		}
		if oldMasterKey != "" {
			os.Setenv("STARPORT_SECURITY_MASTER_KEY", oldMasterKey)
		} else {
			os.Unsetenv("STARPORT_SECURITY_MASTER_KEY")
		}
		if oldBootstrapKey != "" {
			os.Setenv("STARPORT_SECURITY_BOOTSTRAP_API_KEY", oldBootstrapKey)
		} else {
			os.Unsetenv("STARPORT_SECURITY_BOOTSTRAP_API_KEY")
		}
		if oldProviderKey != "" {
			os.Setenv("STARPORT_PROVIDERS_OPENAI_API_KEY", oldProviderKey)
		} else {
			os.Unsetenv("STARPORT_PROVIDERS_OPENAI_API_KEY")
		}
	}()

	// Create a context with timeout to prevent hanging
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done := make(chan bool)
	started := make(chan bool, 1)
	var runErr error

	go func() {
		// Notify that we've started
		started <- true
		runErr = runAppWithArgs(ctx, args)
		done <- true
	}()

	// Wait for the command to start
	<-started

	// Give it a moment to initialize
	time.Sleep(200 * time.Millisecond)

	// Cancel the context to trigger shutdown
	cancel()

	select {
	case <-done:
		// Command finished
		// We expect either nil (clean shutdown) or context.DeadlineExceeded
		if runErr != nil && runErr != context.DeadlineExceeded && runErr != context.Canceled {
			t.Errorf("serve command failed unexpectedly: %v", runErr)
		}
	case <-time.After(10 * time.Second):
		// This is a very generous timeout - if we hit this, something is seriously wrong
		t.Error("serve command did not shut down within 10 seconds after context cancellation")
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
