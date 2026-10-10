package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	starportcli "github.com/agentstation/starport/internal/cli"
)

// The README promises that the catalog commands need no credential or network
// access. Starmap has no offline setting, so the strongest block that the code
// permits is a child test process. The child gets a minimal environment with no
// credentials, a scratch home, and proxy settings that point at a counting
// loopback sentinel. In the child, the default HTTP transport and the default
// resolver refuse and count every dial. A direct dial through a private
// net.Dialer escapes these blocks. The sentinel catches a private transport
// that reads the proxy settings.
const modelsOfflineChildEnvironment = "STARPORT_MODELS_OFFLINE_CHILD"

func TestModelsSearchAndShowWithoutCredentialsOrNetwork(t *testing.T) {
	if os.Getenv(modelsOfflineChildEnvironment) == "1" {
		runOfflineModelsChild(t)
		return
	}
	if testing.Short() {
		t.Skip("embedded catalog decode is slow")
	}

	sentinel, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the proxy sentinel: %v", err)
	}
	var proxied atomic.Int64
	go func() {
		for {
			connection, err := sentinel.Accept()
			if err != nil {
				return
			}
			proxied.Add(1)
			_ = connection.Close()
		}
	}()
	t.Cleanup(func() { _ = sentinel.Close() })

	home := t.TempDir()
	temporary := filepath.Join(home, "tmp")
	if err := os.Mkdir(temporary, 0o700); err != nil {
		t.Fatal(err)
	}
	proxy := "http://" + sentinel.Addr().String()
	environment := []string{
		modelsOfflineChildEnvironment + "=1",
		"HOME=" + home, "USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, "xdg-config"), "XDG_DATA_HOME=" + filepath.Join(home, "xdg-data"),
		"XDG_STATE_HOME=" + filepath.Join(home, "xdg-state"), "XDG_CACHE_HOME=" + filepath.Join(home, "xdg-cache"),
		"APPDATA=" + filepath.Join(home, "appdata"), "LOCALAPPDATA=" + filepath.Join(home, "localappdata"),
		"TMPDIR=" + temporary, "TMP=" + temporary, "TEMP=" + temporary,
	}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		environment = append(environment, name+"="+proxy)
	}
	// Windows needs SYSTEMROOT to start a process. Coverage and race reports
	// keep their settings.
	for _, name := range []string{"SYSTEMROOT", "GOCOVERDIR", "GORACE"} {
		if value, present := os.LookupEnv(name); present {
			environment = append(environment, name+"="+value)
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestModelsSearchAndShowWithoutCredentialsOrNetwork$", "-test.v", "-test.count=1")
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("child process: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "--- PASS: TestModelsSearchAndShowWithoutCredentialsOrNetwork") {
		t.Fatalf("child process did not run the catalog commands:\n%s", output)
	}
	if count := proxied.Load(); count != 0 {
		t.Errorf("the catalog commands opened %d proxy connections", count)
	}

	// The commands leave no file in the scratch home.
	err = filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != home && path != temporary {
			t.Errorf("the catalog commands created %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the scratch home: %v", err)
	}
}

// runOfflineModelsChild runs the README catalog commands with network access
// blocked. It does not use t.TempDir, so the parent sees every created file.
func runOfflineModelsChild(t *testing.T) {
	var refused atomic.Int64
	refuse := func(_ context.Context, network, address string) (net.Conn, error) {
		refused.Add(1)
		return nil, errors.New("network access is blocked: " + network + " " + address)
	}
	http.DefaultTransport = &http.Transport{DialContext: refuse}
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: refuse}

	commands := readmeCatalogCommands(t, subsection(t, readReadme(t).section(t, "Quick start"), "Inspect the catalog"))
	if len(commands) != 2 {
		t.Fatalf("README catalog commands = %q", commands)
	}
	run := func(args []string) []byte {
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		code := runContext(
			context.Background(), args, bytes.NewReader(nil), stdout, stderr,
			func(context.Context, starportcli.GatewayOptions, starportcli.ServerOutput) error { return nil },
			noopDevelopmentStarter, noopInitializer,
		)
		if code != 0 {
			t.Fatalf("%s: exit code = %d, stderr = %s", strings.Join(args, " "), code, stderr)
		}
		return stdout.Bytes()
	}

	shown := commands[1][3]
	var search struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(run(commands[0]), &search); err != nil {
		t.Fatalf("decode search output: %v", err)
	}
	found := false
	for _, model := range search.Data {
		found = found || model.ID == shown
	}
	if !found {
		t.Errorf("search for %q does not list %s", commands[0][3], shown)
	}
	var model struct {
		ID     string `json:"id"`
		Object string `json:"object"`
	}
	if err := json.Unmarshal(run(commands[1]), &model); err != nil {
		t.Fatalf("decode show output: %v", err)
	}
	if model.ID != shown || model.Object != "model" {
		t.Errorf("show %s = %+v", shown, model)
	}
	if count := refused.Load(); count != 0 {
		t.Errorf("the catalog commands attempted %d network dials", count)
	}
}
