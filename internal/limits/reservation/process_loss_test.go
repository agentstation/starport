package reservation

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// lossChild reruns this test binary as a child that blocks at a durable boundary.
type lossChild struct {
	command *exec.Cmd
	output  bytes.Buffer
	exited  chan struct{}
	err     error
}

// startLossChild runs the named test in a child process with env added. The
// child may spend half of the time left before the test deadline, and the
// parent keeps the other half to recover and report. A stalled child ends at
// its own timeout with a goroutine dump, so no fixed timer guesses how fast the
// runner's storage is.
func startLossChild(t *testing.T, test string, env ...string) *lossChild {
	t.Helper()
	var timeout time.Duration
	if deadline, ok := t.Deadline(); ok {
		timeout = time.Until(deadline) / 2
	}
	child := &lossChild{command: exec.Command(os.Args[0], "-test.run=^"+test+"$", "-test.timeout="+timeout.String()), exited: make(chan struct{})}
	child.command.Env = append(os.Environ(), env...)
	child.command.Stdout, child.command.Stderr = &child.output, &child.output
	require.NoError(t, child.command.Start())
	go func() {
		child.err = child.command.Wait()
		close(child.exited)
	}()
	t.Cleanup(func() {
		_ = child.command.Process.Kill()
		<-child.exited
	})
	return child
}

// killAtMarker waits until the child writes a nonempty marker file, kills the
// child, and returns the marker. A child that exits first fails the test.
func (c *lossChild) killAtMarker(t *testing.T, marker string) []byte {
	t.Helper()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-c.exited:
			t.Fatalf("child exited before it reached the boundary: %v\n%s", c.err, c.output.String())
		case <-poll.C:
			if data, err := os.ReadFile(marker); err == nil && len(data) != 0 {
				require.NoError(t, c.command.Process.Kill())
				<-c.exited
				require.Error(t, c.err)
				return data
			}
		}
	}
}
