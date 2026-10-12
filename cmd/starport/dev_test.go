package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	starportcli "github.com/agentstation/starport/internal/cli"
)

// TestDevPersistsAcrossRuns runs dev twice on one home. The second run serves
// the key and the file of the first, and the master key does not change.
func TestDevPersistsAcrossRuns(t *testing.T) {
	home, port := firstRunEnvironment(t)
	configFile := filepath.Join(home, "config", "config.env")

	first := &firstRunOutput{}
	var fileID string
	code, stderr := commandUntilReady(t, []string{"starport", "dev", "--no-open"}, port, first,
		func(ctx context.Context, base string) {
			key := printedGatewayKey(first.String())
			if key == "" {
				t.Errorf("run 1 printed no gateway key; stdout = %s", first.String())
				return
			}
			fileID = uploadDevFile(ctx, t, base, key)
		})
	if code != 0 {
		t.Fatalf("run 1 exit code = %d, want 0; stderr = %s", code, stderr)
	}
	firstKey := printedGatewayKey(first.String())
	firstMaster := masterKeyDigest(t, configFile)

	second := &firstRunOutput{}
	fileStatus := 0
	code, stderr = commandUntilReady(t, []string{"starport", "dev", "--no-open"}, port, second,
		func(ctx context.Context, base string) {
			fileStatus = fileStatusWithKey(ctx, t, base, firstKey, fileID)
		})
	if code != 0 {
		t.Fatalf("run 2 exit code = %d, want 0; stderr = %s", code, stderr)
	}

	if secondKey := printedGatewayKey(second.String()); secondKey != "" {
		t.Errorf("run 2 minted a second key: run 1 key sha256 %s, run 2 key sha256 %s",
			digest(firstKey), digest(secondKey))
	}
	if fileStatus != http.StatusOK {
		t.Errorf("run 2 answered the run 1 key and file with status %d, want %d", fileStatus, http.StatusOK)
	}
	if firstMaster == "" {
		t.Error("run 1 wrote no master key to the platform configuration file")
	} else if secondMaster := masterKeyDigest(t, configFile); secondMaster != firstMaster {
		t.Errorf("master key sha256 changed from %s to %s", firstMaster, secondMaster)
	}
}

// TestDevRefusesSharedManagement keeps persistent dev on local management.
// The refusal names serve and comes before any store or file exists.
func TestDevRefusesSharedManagement(t *testing.T) {
	home, _ := firstRunEnvironment(t)
	t.Setenv("STARPORT_CONFIG_MANAGEMENT", "shared")

	assertDevRefused(t, home, "STARPORT_CONFIG_MANAGEMENT")
}

// TestDevRefusesConfiguredStorage keeps persistent dev on the platform
// storage layout, whether the environment or the configuration file moves it.
func TestDevRefusesConfiguredStorage(t *testing.T) {
	elsewhere := t.TempDir()
	tests := []struct {
		name, key, value string
		inFile           bool
	}{
		{name: "Badger path", key: "STARPORT_STORAGE_BADGER_PATH", value: filepath.Join(elsewhere, "badger")},
		{name: "SQLite path", key: "STARPORT_STORAGE_SQL_SQLITE_PATH", value: filepath.Join(elsewhere, "starport.db")},
		{name: "object store", key: "STARPORT_FILES_BACKEND", value: "objectstore"},
		{name: "Valkey recipe", key: "STARPORT_STORAGE_MODE", value: "valkey"},
		{name: "configuration file", key: "STARPORT_STORAGE_BADGER_PATH", value: filepath.Join(elsewhere, "badger"), inFile: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home, _ := firstRunEnvironment(t)
			if test.inFile {
				configDir := filepath.Join(home, "config")
				if err := os.MkdirAll(configDir, 0o700); err != nil {
					t.Fatal(err)
				}
				setting := []byte(test.key + "=" + test.value + "\n")
				if err := os.WriteFile(filepath.Join(configDir, "config.env"), setting, 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv(test.key, test.value)
			}

			assertDevRefused(t, home, test.key)
		})
	}
}

// TestDevEphemeralRemovesScratch keeps the scratch profile behind --ephemeral.
// The run owns one scratch directory, removes it on exit, and writes nothing
// below the home.
func TestDevEphemeralRemovesScratch(t *testing.T) {
	home, port := firstRunEnvironment(t)
	temporary := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, temporary)
	}

	stdout := &firstRunOutput{}
	during := 0
	code, stderr := commandUntilReady(t, []string{"starport", "dev", "--ephemeral", "--no-open"}, port, stdout,
		func(context.Context, string) { during = len(developmentScratch(t, temporary)) })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if during != 1 {
		t.Errorf("scratch directories during the run = %d, want 1", during)
	}
	if left := developmentScratch(t, temporary); len(left) != 0 {
		t.Errorf("scratch directories after exit = %v, want none", left)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Errorf("home entries = %v, %v; want none", entries, err)
	}
	if count := strings.Count(stdout.String(), gatewayKeyLine); count != 1 {
		t.Errorf("gateway key lines = %d, want 1; stdout = %s", count, stdout.String())
	}
}

// assertDevRefused runs dev and requires a refusal that names the setting and
// serve, with no store or configuration file below the home.
func assertDevRefused(t *testing.T, home, setting string) {
	t.Helper()
	// A dev that does not refuse serves until the bound, so the test fails
	// instead of blocking.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	stdout, stderr := &firstRunOutput{}, &firstRunOutput{}
	code := runContext(ctx, []string{"starport", "dev", "--no-open"}, bytes.NewReader(nil), stdout, stderr,
		runServer, startDevelopment, runInitializer)
	if code != starportcli.ExitCodeRuntime {
		t.Fatalf("exit code = %d, want %d; stderr = %s", code, starportcli.ExitCodeRuntime, stderr.String())
	}
	for _, want := range []string{setting, "starport serve", "starport dev --ephemeral"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q; stderr = %s", want, stderr.String())
		}
	}
	for _, path := range []string{filepath.Join("data", "badger"), filepath.Join("data", "sqlite"), filepath.Join("data", "local-admin-token.json")} {
		if _, err := os.Stat(filepath.Join(home, path)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s exists after the refusal: %v", path, err)
		}
	}
	if masterKeyDigest(t, filepath.Join(home, "config", "config.env")) != "" {
		t.Error("the refusal wrote a master key")
	}
	if strings.Contains(stdout.String(), gatewayKeyLine) {
		t.Errorf("the refusal printed a key; stdout = %s", stdout.String())
	}
}

func developmentScratch(t *testing.T, temporary string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(temporary, "starport-dev-*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// commandUntilReady runs one composed command until the gateway reports ready.
// It calls during with the gateway base URL, cancels the command, and returns
// the exit code and the standard error.
func commandUntilReady(
	t *testing.T,
	args []string,
	port int,
	stdout *firstRunOutput,
	during func(ctx context.Context, base string),
) (int, string) {
	t.Helper()
	return developmentUntilReady(t, startDevelopment, args, port, stdout, during)
}

// developmentUntilReady runs commandUntilReady with a chosen development
// starter.
func developmentUntilReady(
	t *testing.T,
	development starportcli.DevelopmentStarter,
	args []string,
	port int,
	stdout *firstRunOutput,
	during func(ctx context.Context, base string),
) (int, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stderr := &firstRunOutput{}
	exited := make(chan int, 1)
	go func() {
		exited <- runContext(ctx, args, bytes.NewReader(nil), stdout, stderr,
			runServer, development, runInitializer)
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
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
			if gatewayReady(ctx, base+"/health/ready") {
				if during != nil {
					during(ctx, base)
				}
				cancel()
				return <-exited, stderr.String()
			}
		}
	}
}

// printedGatewayKey returns the key from the one-time key line, or "".
func printedGatewayKey(output string) string {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		if key, found := strings.CutPrefix(scanner.Text(), gatewayKeyLine); found {
			return strings.TrimSpace(key)
		}
	}
	return ""
}

// masterKeyDigest returns a SHA-256 prefix of the configured master key, or
// "" when the file or the key is absent. The key itself never leaves here.
func masterKeyDigest(t *testing.T, configFile string) string {
	t.Helper()
	content, err := os.ReadFile(configFile)
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(content)) {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), "STARPORT_SECURITY_MASTER_KEY="); found && value != "" {
			return digest(value)
		}
	}
	return ""
}

// digest returns a SHA-256 prefix that identifies a secret without showing it.
func digest(secret string) string {
	if secret == "" {
		return "(none)"
	}
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])[:12]
}

func uploadDevFile(ctx context.Context, t *testing.T, base, key string) string {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("purpose", "user_data"); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("file", "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("persistent development\n")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/files", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("upload a file: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	var stored struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if err := json.NewDecoder(response.Body).Decode(&stored); err != nil || stored.ID == "" {
		t.Fatalf("upload response id = %q, err = %v", stored.ID, err)
	}
	return stored.ID
}

func fileStatusWithKey(ctx context.Context, t *testing.T, base, key, fileID string) int {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/files/"+fileID+"/content", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("read the file: %v", err)
	}
	_ = response.Body.Close()
	return response.StatusCode
}
