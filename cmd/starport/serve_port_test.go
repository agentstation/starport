package main

import (
	"bytes"
	"context"
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

// runUntilExit runs one composed command to its exit within a minute and
// returns the exit code, the standard output, and the standard error.
func runUntilExit(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	stdout, stderr := &firstRunOutput{}, &firstRunOutput{}
	code := runContext(ctx, append([]string{"starport"}, args...), bytes.NewReader(nil), stdout, stderr,
		runServer, startDevelopment, runInitializer)
	if ctx.Err() != nil {
		t.Fatalf("starport %s did not exit within a minute", strings.Join(args, " "))
	}
	return code, stdout.String(), stderr.String()
}
