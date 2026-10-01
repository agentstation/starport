package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

const adoptValkeyImage = "valkey/valkey:7-alpine"

// adoptValkey is one private Valkey process with persistent data that the test owns.
// Restart and promotion tests change this process only. Shared fixtures stay unchanged.
type adoptValkey struct {
	docker, name, network string
	url                   string
}

func adoptDocker(t *testing.T) string {
	t.Helper()
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skipf("UNVERIFIED: a private Valkey process needs Docker: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, docker, "image", "inspect", adoptValkeyImage).CombinedOutput(); err != nil { // #nosec G204 -- The test selects a fixed image.
		t.Skipf("UNVERIFIED: the private Valkey image is unavailable: %v: %s", err, bytes.TrimSpace(output))
	}
	return docker
}

func adoptRun(t *testing.T, docker string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, docker, arguments...).CombinedOutput() // #nosec G204 -- The test builds each Docker argument.
	require.NoError(t, err, "docker %s: %s", arguments[0], bytes.TrimSpace(output))
	return strings.TrimSpace(string(output))
}

// startAdoptValkey starts a private primary with append-only persistence on a private volume and removes both in cleanup.
func startAdoptValkey(t *testing.T) *adoptValkey {
	t.Helper()
	docker := adoptDocker(t)
	suffix := strings.ToLower(rand.Text())[:12]
	v := &adoptValkey{docker: docker, name: "starport-adopt-" + suffix, network: "starport-adopt-" + suffix}
	adoptRun(t, docker, "network", "create", v.network)
	t.Cleanup(func() { adoptRun(t, docker, "network", "rm", v.network) })
	v.url = v.start(t, v.name, "")
	return v
}

// start runs one Valkey process on the private network. A non-empty primary starts a replica of that process.
// The fixed loopback port keeps the configured address across a restart.
func (v *adoptValkey) start(t *testing.T, name, primary string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).AddrPort().Port()
	require.NoError(t, listener.Close())
	arguments := []string{"run", "-d", "--name", name, "--network", v.network, "-v", name + ":/data", "-p", fmt.Sprintf("127.0.0.1:%d:6379", port), adoptValkeyImage,
		"valkey-server", "--appendonly", "yes", "--appendfsync", "always", "--save", ""}
	if primary != "" {
		arguments = append(arguments, "--replicaof", primary, "6379")
	}
	adoptRun(t, v.docker, arguments...)
	t.Cleanup(func() {
		adoptRun(t, v.docker, "rm", "-f", "-v", name)
		adoptRun(t, v.docker, "volume", "rm", "-f", name)
	})
	return v.ready(t, name)
}

// ready returns the published address after the process answers and loads its data.
func (v *adoptValkey) ready(t *testing.T, name string) string {
	t.Helper()
	port := adoptRun(t, v.docker, "port", name, "6379/tcp")
	port = port[strings.LastIndex(port, ":")+1:]
	address := "redis://127.0.0.1:" + strings.Fields(port)[0]
	deadline := time.Now().Add(time.Minute)
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		output, err := exec.CommandContext(ctx, v.docker, "exec", name, "valkey-cli", "INFO", "persistence").CombinedOutput() // #nosec G204 -- The test owns the container name.
		cancel()
		if err == nil && strings.Contains(string(output), "loading:0") {
			return address
		}
		require.True(t, time.Now().Before(deadline), "private Valkey did not become ready")
		time.Sleep(200 * time.Millisecond)
	}
}

// restart stops and starts the same process with the same persistent volume and address.
func (v *adoptValkey) restart(t *testing.T) {
	t.Helper()
	adoptRun(t, v.docker, "restart", v.name)
	require.Equal(t, v.url, v.ready(t, v.name))
}

// replica starts a synchronized replica of the primary and returns its name and address.
func (v *adoptValkey) replica(t *testing.T) (string, string) {
	t.Helper()
	name := v.name + "-replica"
	address := v.start(t, name, v.name)
	deadline := time.Now().Add(time.Minute)
	for {
		output := adoptRun(t, v.docker, "exec", name, "valkey-cli", "INFO", "replication")
		if strings.Contains(output, "master_link_status:up") && strings.Contains(output, "master_sync_in_progress:0") {
			return name, address
		}
		require.True(t, time.Now().Before(deadline), "private replica did not synchronize")
		time.Sleep(200 * time.Millisecond)
	}
}

// promote waits until the replica holds every primary write and then makes it a primary.
func (v *adoptValkey) promote(t *testing.T, replica string) {
	t.Helper()
	adoptRun(t, v.docker, "exec", v.name, "valkey-cli", "WAIT", "1", "10000")
	adoptRun(t, v.docker, "exec", replica, "valkey-cli", "REPLICAOF", "NO", "ONE")
}

// adoptIncarnation observes the process identity at one address with the deployment settings of cfg.
func adoptIncarnation(t *testing.T, cfg *config.Config, address string) string {
	t.Helper()
	selected := cfg.RuntimeStorage().Valkey
	selected.URL = address
	store, err := storage.OpenValkey(selected)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	incarnation, err := store.(storage.IncarnationProvider).ObserveIncarnation(t.Context())
	require.NoError(t, err)
	return incarnation
}
