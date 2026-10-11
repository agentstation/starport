package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/config"
)

func TestFirstRunQualifiesOnlyALoopbackLocalPlatformRoot(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	for _, test := range []struct {
		name string
		edit map[string]string
		want bool
	}{
		{name: "clean local root", want: true},
		{name: "supplied master key", edit: map[string]string{"STARPORT_SECURITY_MASTER_KEY": strings.Repeat("k", 32)}},
		{name: "external management", edit: map[string]string{"STARPORT_CONFIG_MANAGEMENT": config.ManagementExternal}},
		{name: "network address", edit: map[string]string{"STARPORT_SERVER_HOST": "0.0.0.0"}},
		{name: "configured Badger path", edit: map[string]string{"STARPORT_STORAGE_BADGER_PATH": filepath.Join(t.TempDir(), "badger")}},
		{name: "configured SQLite path", edit: map[string]string{"STARPORT_STORAGE_SQL_SQLITE_PATH": filepath.Join(t.TempDir(), "starport.db")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := config.NewLoader().WithPaths(paths).WithEnvFiles().WithEnvironment(test.edit).Load(t.Context())
			if err != nil {
				t.Fatalf("load configuration: %v", err)
			}
			if got := FirstRun(cfg, paths); got != test.want {
				t.Errorf("FirstRun = %t, want %t", got, test.want)
			}
		})
	}
}

func TestInitializeFirstRunNamesRecoveryForPartialState(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	if err := os.MkdirAll(paths.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.ConfigDir, "stray"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := New(paths).InitializeFirstRun(context.Background(), Request{APIKeyName: "local-admin"})

	if !errors.Is(err, ErrPartialState) || !strings.Contains(err.Error(), RecoverCommand) {
		t.Fatalf("partial state error = %v", err)
	}
	if result.APIKey != "" {
		t.Error("a partial root returned a credential")
	}
	if _, err := os.Stat(paths.ConfigFile); !os.IsNotExist(err) {
		t.Errorf("configuration file after refusal: %v", err)
	}
}

func TestInitializeFirstRunLeavesAReadyRootAlone(t *testing.T) {
	paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
	service := New(paths)
	if _, err := service.Initialize(context.Background(), Request{APIKeyName: "local-admin"}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	before := mustReadFile(t, paths.ConfigFile)

	result, err := service.InitializeFirstRun(context.Background(), Request{APIKeyName: "local-admin"})

	if err != nil || result.APIKey != "" {
		t.Fatalf("ready root result = %#v, error = %v", result, err)
	}
	if string(mustReadFile(t, paths.ConfigFile)) != string(before) {
		t.Error("a ready root's configuration changed")
	}
}
