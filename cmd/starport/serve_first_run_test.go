package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	gatewayKeyLine = "Gateway API key (shown once):"
	welcomeLine    = "Welcome to Starport."

	// readyDeadline bounds a first-run start under the race detector on a
	// shared runner. The first start took 30 s to 61 s across the CI runners,
	// so the deadline keeps a wide margin. A ready gateway returns at once.
	readyDeadline = 5 * time.Minute
)

func TestServeInitializesEmptyStorage(t *testing.T) {
	home, port := firstRunEnvironment(t)
	stdout := &firstRunOutput{configFile: filepath.Join(home, "config", "config.env")}

	code, stderr := serveUntilReady(t, port, stdout)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
	}
	config, err := os.ReadFile(stdout.configFile)
	if err != nil {
		t.Fatalf("read the generated configuration: %v", err)
	}
	if !strings.Contains(string(config), "STARPORT_SECURITY_MASTER_KEY=") {
		t.Error("the generated configuration holds no master key")
	}
	if info, err := os.Stat(filepath.Join(home, "data", "badger")); err != nil || !info.IsDir() {
		t.Errorf("Badger store = %v, %v; want a directory", info, err)
	}
	if count := strings.Count(stdout.String(), gatewayKeyLine); count != 1 {
		t.Errorf("gateway key lines = %d, want 1; stdout = %s", count, stdout.String())
	}
}

func TestServeGreetsAfterInitialization(t *testing.T) {
	home, port := firstRunEnvironment(t)
	stdout := &firstRunOutput{configFile: filepath.Join(home, "config", "config.env")}

	code, stderr := serveUntilReady(t, port, stdout)
	if stdout.greeted && !stdout.configuredAtGreeting {
		t.Error("serve greeted before initialization wrote the configuration")
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if !stdout.greeted {
		t.Fatalf("serve did not greet; stdout = %s", stdout.String())
	}
	output := stdout.String()
	key, greeting := strings.Index(output, gatewayKeyLine), strings.Index(output, welcomeLine)
	if key < 0 || greeting < key {
		t.Errorf("key line at %d, greeting at %d; want the greeting after the key", key, greeting)
	}
	if _, err := os.Stat(filepath.Join(home, "data", "welcomed")); err != nil {
		t.Errorf("welcome stamp: %v", err)
	}
}

// firstRunEnvironment points every platform root at an empty temporary home
// and returns the home and a free loopback port.
func firstRunEnvironment(t *testing.T) (string, int) {
	t.Helper()
	home := t.TempDir()
	port := freeLoopbackPort(t)
	for _, name := range []string{
		"STARPORT_CONFIG_DIR", "STARPORT_DATA_DIR", "STARPORT_STATE_ROOT", "STARPORT_CACHE_DIR",
		"STARPORT_CONFIG_FILE", "STARPORT_CONFIG_MANAGEMENT", "STARPORT_SECURITY_MASTER_KEY", "STARPORT_SERVER_HOST",
	} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("STARPORT_HOME", home)
	t.Setenv("STARPORT_SERVER_PORT", strconv.Itoa(port))
	t.Setenv("STARPORT_CATALOG_NETWORK_MODE", "offline")
	t.Setenv("STARPORT_CATALOG_ACQUISITION_ENABLED", "false")
	return home, port
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release the port: %v", err)
	}
	return port
}

// serveUntilReady runs the composed serve command until the gateway reports
// ready, then cancels it. It returns the exit code and the standard error.
func serveUntilReady(t *testing.T, port int, stdout *firstRunOutput) (int, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stderr := &firstRunOutput{}
	exited := make(chan int, 1)
	go func() {
		exited <- runContext(ctx, []string{"starport", "serve"}, bytes.NewReader(nil), stdout, stderr,
			runServer, noopDevelopmentStarter, runInitializer)
	}()

	ready := fmt.Sprintf("http://127.0.0.1:%d/health/ready", port)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(readyDeadline)
	for {
		select {
		case code := <-exited:
			return code, stderr.String()
		case <-deadline:
			cancel()
			t.Fatalf("gateway did not become ready; exit code = %d", <-exited)
		case <-ticker.C:
			if gatewayReady(ctx, ready) {
				cancel()
				return <-exited, stderr.String()
			}
		}
	}
}

func gatewayReady(ctx context.Context, url string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	return response.StatusCode == http.StatusOK
}

// firstRunOutput records the command output. When the greeting arrives, it
// also records whether initialization had already written the configuration.
type firstRunOutput struct {
	mu                   sync.Mutex
	buffer               bytes.Buffer
	configFile           string
	greeted              bool
	configuredAtGreeting bool
}

func (o *firstRunOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.configFile != "" && bytes.Contains(p, []byte(welcomeLine)) {
		_, err := os.Stat(o.configFile)
		o.greeted, o.configuredAtGreeting = true, err == nil
	}
	return o.buffer.Write(p)
}

func (o *firstRunOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buffer.String()
}
