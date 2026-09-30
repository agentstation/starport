package app

import (
	"context"
	legacyjson "encoding/json"
	"encoding/json/v2"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

const freshHistoryChild = "STARPORT_RECOVERY_FRESH_HISTORY_CHILD"

type freshHistoryReply struct {
	Started     bool
	SQLImport   bool
	KVImport    bool
	Closed      bool
	Conflict    bool
	Incarnation bool
	OtherError  bool
}

// This child uses normal construction with the fixture's decoded configuration.
// It does not accept history, activate imports, or replace runtime factories.
func runFreshHistoryChild(t *testing.T, path string) {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var fixture freshGatewayFixture
	require.NoError(t, json.Unmarshal(body, &fixture))
	application, constructionErr := New(freshGatewayConfiguration(t, fixture))
	reply := freshHistoryReply{Started: constructionErr == nil}
	if constructionErr == nil {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		require.NotNil(t, application)
		require.NoError(t, application.Close(ctx))
	} else {
		require.Nil(t, application)
		reply.SQLImport = errors.Is(constructionErr, sqlstore.ErrImportRestricted)
		reply.KVImport = errors.Is(constructionErr, storage.ErrImportRestricted)
		reply.Closed = errors.Is(constructionErr, recovery.ErrClosed)
		reply.Conflict = errors.Is(constructionErr, recovery.ErrConflict)
		reply.Incarnation = errors.Is(constructionErr, storage.ErrIncarnationChanged)
		reply.OtherError = !reply.SQLImport && !reply.KVImport && !reply.Closed && !reply.Conflict && !reply.Incarnation
	}
	encoded, err := json.Marshal(reply)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(fixture.Report, encoded, 0600))
}

func freshHistoryConstruction(t *testing.T, cfg *config.Config) freshHistoryReply {
	t.Helper()
	root := freshHistoryPrivateDirectory(t)
	body, err := json.Marshal(cfg, legacyjson.FormatDurationAsNano(true), json.Deterministic(true))
	require.NoError(t, err)
	fixture := freshGatewayFixture{Paths: cfg.EffectivePaths(), Config: body, Catalog: cfg.Catalog.CatalogValues(), Report: filepath.Join(root, "reply.json")}
	encoded, err := json.Marshal(fixture, json.Deterministic(true))
	require.NoError(t, err)
	path := filepath.Join(root, "fixture.json")
	require.NoError(t, os.WriteFile(path, encoded, 0600))
	logPath := filepath.Join(root, "child.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	require.NoError(t, err)
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRecoveryFreshHistoryProcess$", "-test.timeout=2m")
	command.Env = append(os.Environ(), freshHistoryChild+"="+path)
	command.Stdout, command.Stderr = log, log
	processErr := command.Run()
	require.NoError(t, log.Close())
	if processErr != nil {
		t.Fatalf("fresh history process failed: %v; private log: %s", processErr, logPath)
	}
	body, err = os.ReadFile(fixture.Report)
	require.NoError(t, err)
	var reply freshHistoryReply
	require.NoError(t, json.Unmarshal(body, &reply))
	return reply
}

func TestRecoveryFreshHistoryProcess(t *testing.T) {
	if path := os.Getenv(freshHistoryChild); path != "" {
		runFreshHistoryChild(t, path)
	}
}
