package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	starportcli "github.com/agentstation/starport/internal/cli"
)

// TestServeFailsWhenPortIsBusy keeps serve on its configured port. When
// another process holds the port, serve exits 1 and names the port.
func TestServeFailsWhenPortIsBusy(t *testing.T) {
	firstRunEnvironment(t)
	busy := holdLoopbackPort(t)
	t.Setenv("STARPORT_SERVER_PORT", strconv.Itoa(busy))

	code, stdout, stderr := runUntilExit(t, "serve")
	if code != starportcli.ExitCodeRuntime {
		t.Errorf("exit code = %d, want %d; stderr = %s", code, starportcli.ExitCodeRuntime, stderr)
	}
	if want := "port " + strconv.Itoa(busy) + " is busy"; !strings.Contains(stderr, want) {
		t.Errorf("stderr does not name the busy port %q; stderr = %s", want, stderr)
	}
	if strings.Contains(stdout, "is busy. Bound port") {
		t.Errorf("serve fell back to another port; stdout = %s", stdout)
	}
}

// TestServeBindsBeforeTheFirstRunKey refuses a busy port on a clean root
// before the one-time key exists, so the refused run leaves the root clean and
// a later run prints the key once.
func TestServeBindsBeforeTheFirstRunKey(t *testing.T) {
	home, free := firstRunEnvironment(t)
	busy := holdLoopbackPort(t)
	t.Setenv("STARPORT_SERVER_PORT", strconv.Itoa(busy))
	configFile := filepath.Join(home, "config", "config.env")

	code, stdout, stderr := runUntilExit(t, "serve")
	if code != starportcli.ExitCodeRuntime {
		t.Errorf("exit code = %d, want %d; stderr = %s", code, starportcli.ExitCodeRuntime, stderr)
	}
	if want := "port " + strconv.Itoa(busy) + " is busy"; !strings.Contains(stderr, want) {
		t.Errorf("stderr does not name the busy port %q; stderr = %s", want, stderr)
	}
	if strings.Contains(stdout, gatewayKeyLine) {
		t.Error("serve printed a first-run key for a gateway that never bound")
	}
	if masterKeyDigest(t, configFile) != "" {
		t.Error("the refused run initialized the root")
	}
	if _, err := os.Stat(filepath.Join(home, "data", "badger")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the refused run created the Badger store: %v", err)
	}

	t.Setenv("STARPORT_SERVER_PORT", strconv.Itoa(free))
	output := &firstRunOutput{}
	if code, stderr := commandUntilReady(t, []string{"starport", "serve"}, free, output, nil); code != 0 {
		t.Fatalf("later run exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if count := strings.Count(output.String(), gatewayKeyLine); count != 1 {
		t.Errorf("later run printed %d first-run key lines, want 1", count)
	}
}

// runUntilExit runs one composed command to its exit within a minute and
// returns the exit code, the standard output, and the standard error.
func runUntilExit(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return developmentUntilExit(t, startDevelopment, args...)
}

// developmentUntilExit runs runUntilExit with a chosen development starter.
func developmentUntilExit(t *testing.T, development starportcli.DevelopmentStarter, args ...string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	stdout, stderr := &firstRunOutput{}, &firstRunOutput{}
	code := runContext(ctx, append([]string{"starport"}, args...), bytes.NewReader(nil), stdout, stderr,
		runServer, development, runInitializer)
	if ctx.Err() != nil {
		t.Fatalf("starport %s did not exit within a minute", strings.Join(args, " "))
	}
	return code, stdout.String(), stderr.String()
}
