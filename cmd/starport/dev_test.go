package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stderr := &firstRunOutput{}
	exited := make(chan int, 1)
	go func() {
		exited <- runContext(ctx, args, bytes.NewReader(nil), stdout, stderr,
			runServer, startDevelopment, runInitializer)
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
