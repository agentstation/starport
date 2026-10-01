package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

const recoveryGatewayChild = "STARPORT_RECOVERY_GATEWAY_BARRIER_CHILD"
const recoveryGatewayReplyPrefix = "RECOVERY-GATEWAY-REPLY:"

type recoveryGatewayReply struct {
	Started     bool
	Closed      bool
	Conflict    bool
	Incarnation bool
	OtherError  bool
	BaseURL     string
	Reachable   bool
	Rules       int
}

func recoveryGatewayError(err error) recoveryGatewayReply {
	return recoveryGatewayReply{Closed: errors.Is(err, recovery.ErrClosed), Conflict: errors.Is(err, recovery.ErrConflict), Incarnation: errors.Is(err, storage.ErrIncarnationChanged), OtherError: err != nil && !errors.Is(err, recovery.ErrClosed) && !errors.Is(err, recovery.ErrConflict) && !errors.Is(err, storage.ErrIncarnationChanged)}
}

// runRecoveryGatewayChild uses normal construction and the shipped HTTP router.
// The parent changes native ownership while this same process remains alive.
func runRecoveryGatewayChild(t *testing.T, path string) {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var input budgetFleetInput
	require.NoError(t, json.Unmarshal(body, &input))
	reply := func(value recoveryGatewayReply) {
		body, err := json.Marshal(value)
		require.NoError(t, err)
		_, err = fmt.Fprintln(os.Stdout, recoveryGatewayReplyPrefix+string(body))
		require.NoError(t, err)
	}
	application, err := New(budgetFleetConfig(t, input))
	if err != nil {
		require.Nil(t, application)
		reply(recoveryGatewayError(err))
		return
	}
	defer func() { require.NoError(t, application.Close(context.Background())) }()
	runtime, ok := application.httpServer.(*server.Server)
	require.True(t, ok)
	gateway := httptest.NewServer(runtime.Router())
	defer gateway.Close()
	reply(recoveryGatewayReply{Started: true, BaseURL: gateway.URL})
	commands := bufio.NewScanner(os.Stdin)
	for commands.Scan() {
		switch commands.Text() {
		case "observe":
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			observed := recoveryGatewayError(application.budget.shared.Check(ctx))
			observed.Reachable = application.store.Ping(ctx) == nil
			cancel()
			reply(observed)
		case "policy":
			sum := sha256.Sum256([]byte(performanceGatewayKey))
			digest := hex.EncodeToString(sum[:])
			bundle, err := application.authorization.cache.Resolve(t.Context(), authorization.Identity{Subject: digest})
			require.NoError(t, err)
			reply(recoveryGatewayReply{Rules: bundle.BudgetPolicy().RuleCount()})
		case "finish":
			return
		default:
			t.Fatal("unknown private gateway command")
		}
	}
	require.NoError(t, commands.Err())
}

type recoveryGatewayProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
	replies chan recoveryGatewayReply
	done    chan struct{}
	waitErr error
	logPath string
}

func startRecoveryGatewayProcess(t *testing.T, input budgetFleetInput) (*recoveryGatewayProcess, recoveryGatewayReply) {
	t.Helper()
	root := t.TempDir()
	body, err := json.Marshal(input)
	require.NoError(t, err)
	path := filepath.Join(root, "fixture.json")
	require.NoError(t, os.WriteFile(path, body, 0600))
	child := &recoveryGatewayProcess{command: exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRecoveryGatewayHistoryBarrierProcess$", "-test.timeout="+budgetFleetChildTimeout.String()), replies: make(chan recoveryGatewayReply, 8), done: make(chan struct{}), logPath: filepath.Join(root, "child.log")}
	child.command.Env = append(os.Environ(), recoveryGatewayChild+"="+path)
	log, err := os.OpenFile(child.logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	require.NoError(t, err)
	child.command.Stderr = log
	child.input, err = child.command.StdinPipe()
	require.NoError(t, err)
	output, err := child.command.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, child.command.Start())
	go func() {
		reader := bufio.NewScanner(output)
		for reader.Scan() {
			line := reader.Text()
			_, _ = fmt.Fprintln(log, line)
			if encoded, ok := strings.CutPrefix(line, recoveryGatewayReplyPrefix); ok {
				var value recoveryGatewayReply
				if json.Unmarshal([]byte(encoded), &value) == nil {
					child.replies <- value
				}
			}
		}
		close(child.replies)
		child.waitErr = child.command.Wait()
		_ = log.Close()
		close(child.done)
	}()
	t.Cleanup(func() {
		_ = child.input.Close()
		select {
		case <-child.done:
		case <-time.After(5 * time.Second):
			_ = child.command.Process.Kill()
			<-child.done
		}
	})
	return child, child.reply(t, budgetFleetReadinessTimeout)
}

func (p *recoveryGatewayProcess) reply(t *testing.T, timeout time.Duration) recoveryGatewayReply {
	t.Helper()
	select {
	case value, ok := <-p.replies:
		if ok {
			return value
		}
	case <-time.After(timeout):
	case <-t.Context().Done():
	}
	t.Fatalf("gateway process returned no bounded reply; private log: %s", p.logPath)
	return recoveryGatewayReply{}
}

func (p *recoveryGatewayProcess) exchange(t *testing.T, command string) recoveryGatewayReply {
	t.Helper()
	_, err := fmt.Fprintln(p.input, command)
	require.NoError(t, err)
	return p.reply(t, 10*time.Second)
}

func (p *recoveryGatewayProcess) finish(t *testing.T) {
	t.Helper()
	_, err := fmt.Fprintln(p.input, "finish")
	require.NoError(t, err)
	require.NoError(t, p.input.Close())
	select {
	case <-p.done:
		require.NoError(t, p.waitErr)
	case <-time.After(10 * time.Second):
		t.Fatal("gateway process did not stop after its completed admission checks")
	}
}

func recoveryGatewayRequest(t *testing.T, base string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	status, body, err := budgetRequest(t.Context(), &performanceFixture{gateway: &httptest.Server{URL: base}, client: client})
	require.NoError(t, err)
	return status, body
}

func TestRecoveryGatewayHistoryBarrierProcess(t *testing.T) {
	if path := os.Getenv(recoveryGatewayChild); path != "" {
		runRecoveryGatewayChild(t, path)
	}
}

func recoveryGatewayHealth(t *testing.T, base string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	for path, expected := range map[string]int{"/health/ready": http.StatusServiceUnavailable, "/health/live": http.StatusOK} {
		response, err := client.Get(base + path)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, expected, response.StatusCode, path)
	}
}
