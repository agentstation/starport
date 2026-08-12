package diagnosis

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/identity"
	providerauth "github.com/agentstation/starport/internal/providers/auth"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/setup"
	"github.com/agentstation/starport/internal/storage"
)

func TestSyntheticCatalogProviderOperatorSurfaces(t *testing.T) {
	embedded, err := catalogs.NewEmbedded()
	require.NoError(t, err)
	baseline, err := embedded.Build()
	require.NoError(t, err)
	provider, err := baseline.Provider(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	provider.ID = "acme"
	provider.Aliases = nil
	provider.Name = "Acme"
	for index := range provider.Credentials.Fields {
		provider.Credentials.Fields[index].Environment = nil
	}
	provider.Credentials.Fields[0].Environment = []string{"ACME_API_KEY"}
	builder, err := catalogs.NewBuilderFrom(baseline)
	require.NoError(t, err)
	require.NoError(t, builder.SetProvider(provider))
	catalog, err := builder.Build()
	require.NoError(t, err)

	cfg, err := config.NewLoader().
		WithPaths(config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))).
		WithEnvironment(map[string]string{
			"STARPORT_SECURITY_MASTER_KEY": strings.Repeat("master-secret-", 3),
			"ACME_API_KEY":                 "acme-secret",
		}).
		WithEnvFiles().
		Load(t.Context())
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(staticSource{state: starmap.CatalogState{
		Catalog: catalog, GenerationID: "synthetic-acme", Sequence: 1,
	}})
	require.NoError(t, err)
	report := Report{OK: true}
	testService(cfg).checkAdapters(t.Context(), cfg, plane, catalog, &report)
	require.True(t, report.OK, "%#v", report)
	check := assertCheck(t, report, "adapters", StatusPass)
	require.Contains(t, check.Message, "executable provider adapters")
}

func TestOfflineDiagnosisIsPassiveAndRedactsSecrets(t *testing.T) {
	root := filepath.Join(t.TempDir(), "starport")
	cfg := loadTestConfig(t, root)
	report := testService(cfg).run(context.Background(), Options{})
	if !report.OK {
		t.Fatalf("report = %#v", report)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("offline diagnosis changed filesystem state: %v", err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	providerSecret, _ := cfg.Providers[catalogs.ProviderIDOpenAI].Material.Value("api-key")
	for _, secret := range []string{cfg.Security.MasterKey, providerSecret} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("diagnosis output contains secret %q", secret)
		}
	}
	assertCheck(t, report, "storage", StatusSkip)
	assertCheck(t, report, "catalog", StatusPass)
	assertCheck(t, report, "adapters", StatusPass)
}

func TestProbeReadsIdentityWithoutChangingIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "starport")
	cfg := loadTestConfig(t, root)
	store, err := storage.Open(cfg.Storage.RuntimeStorage())
	if err != nil {
		t.Fatal(err)
	}
	issued, err := setup.InitializeIdentity(context.Background(), store, "doctor-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before := treeFingerprint(t, root)

	report := testService(cfg).run(context.Background(), Options{Probe: true})
	if !report.OK {
		t.Fatalf("report = %#v", report)
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		assertCheck(t, report, "storage", StatusSkip)
		assertCheck(t, report, "identities", StatusSkip)
		if after := treeFingerprint(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("unsupported diagnosis changed storage files\nbefore: %#v\nafter: %#v", before, after)
		}
		return
	}
	assertCheck(t, report, "storage", StatusPass)
	assertCheck(t, report, "identities", StatusPass)
	if after := treeFingerprint(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("diagnosis changed storage files\nbefore: %#v\nafter: %#v", before, after)
	}

	reopened, err := storage.OpenReadOnly(cfg.Storage.RuntimeStorage())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	repository, err := identity.Open(reopened)
	if err != nil {
		t.Fatal(err)
	}
	records, err := repository.List(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].APIKey.ID != issued.APIKey.ID {
		t.Fatalf("identities after diagnosis = %#v", records)
	}
}

func TestProbeReportsExactEmptyIdentityCheck(t *testing.T) {
	root := filepath.Join(t.TempDir(), "starport")
	cfg := loadTestConfig(t, root)
	service := testService(cfg)
	service.dependencies.openStorage = func(storage.Config) (storage.KVStore, error) {
		return storage.NewMockStore(), nil
	}
	report := service.run(context.Background(), Options{Probe: true})
	if report.OK {
		t.Fatalf("report = %#v", report)
	}
	check := assertCheck(t, report, "identities", StatusFail)
	if !strings.Contains(check.Message, "starport init") {
		t.Errorf("identity failure = %q", check.Message)
	}
}

func TestDiagnosisRedactsSecretsFromDependencyFailures(t *testing.T) {
	root := filepath.Join(t.TempDir(), "starport")
	cfg := loadTestConfig(t, root)
	resolveEmbeddedProviders(t, cfg)
	openAI := cfg.Providers[catalogs.ProviderIDOpenAI]
	openAISecret, found := openAI.Material.Value("api-key")
	if !found {
		t.Fatal("OpenAI inference material is missing")
	}
	openAI.BaseURL = "https://" + "url-user:url-password" + "@provider.example?token=" +
		openAISecret + "#base-url-secret"
	cfg.Providers[catalogs.ProviderIDOpenAI] = openAI
	service := testService(cfg)
	service.dependencies.transports = func() (*connectors.TransportRegistry, error) {
		return nil, errors.New(
			"failure " + openAISecret + " " +
				openAI.BaseURL + " " + cfg.Security.MasterKey +
				" url-password base-url-secret",
		)
	}
	report := service.run(context.Background(), Options{})
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		openAISecret,
		openAI.BaseURL,
		cfg.Security.MasterKey,
		"url-password",
		"base-url-secret",
	} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("diagnosis failure output contains secret %q", secret)
		}
	}
	check := assertCheck(t, report, "adapters", StatusFail)
	if check.Message != "provider transport registry could not be created" {
		t.Errorf("adapter failure = %q", check.Message)
	}
}

func TestBadgerRecoveryMakesProbeInconclusive(t *testing.T) {
	cfg := loadTestConfig(t, filepath.Join(t.TempDir(), "starport"))
	service := testService(cfg)
	service.dependencies.openStorage = func(storage.Config) (storage.KVStore, error) {
		return nil, storage.ErrReadOnlyRecoveryRequired
	}

	report := service.run(context.Background(), Options{Probe: true})
	storageCheck := assertCheck(t, report, "storage", StatusSkip)
	if !strings.Contains(storageCheck.Message, "starport serve") {
		t.Errorf("storage recovery guidance = %q", storageCheck.Message)
	}
	assertCheck(t, report, "identities", StatusSkip)
}

func TestUnsupportedReadOnlyStorageMakesProbeInconclusive(t *testing.T) {
	cfg := loadTestConfig(t, filepath.Join(t.TempDir(), "starport"))
	service := testService(cfg)
	service.dependencies.openStorage = func(storage.Config) (storage.KVStore, error) {
		return nil, storage.ErrReadOnlyUnsupported
	}

	report := service.run(context.Background(), Options{Probe: true})
	storageCheck := assertCheck(t, report, "storage", StatusSkip)
	if !strings.Contains(storageCheck.Message, "starport serve") {
		t.Errorf("unsupported storage guidance = %q", storageCheck.Message)
	}
	assertCheck(t, report, "identities", StatusSkip)
}

func TestDiagnosisDoesNotTrustConfigurationLoaderErrors(t *testing.T) {
	secret := "loader-secret-that-must-not-appear"
	service := testService(loadTestConfig(t, filepath.Join(t.TempDir(), "starport")))
	service.dependencies.loadConfig = func(context.Context) (*config.Config, error) {
		return nil, errors.New("malformed credential " + secret)
	}

	report := service.run(context.Background(), Options{})
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Errorf("diagnosis output contains loader secret: %s", encoded)
	}
	check := assertCheck(t, report, "configuration", StatusFail)
	if check.Message != "configuration could not be loaded" {
		t.Errorf("configuration failure = %q", check.Message)
	}
}

func loadTestConfig(t *testing.T, root string) *config.Config {
	t.Helper()
	cfg, err := config.NewLoader().
		WithPaths(config.PathsForConfigDir(root)).
		WithEnvironment(map[string]string{
			"STARPORT_SECURITY_MASTER_KEY": strings.Repeat("master-secret-", 3),
			"OPENAI_API_KEY":               "provider-secret-value",
		}).
		Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func resolveEmbeddedProviders(t *testing.T, cfg *config.Config) {
	t.Helper()
	builder, err := catalogs.NewEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ResolveProviders(context.Background(), catalog.Providers()); err != nil {
		t.Fatal(err)
	}
}

func testService(cfg *config.Config) service {
	return service{dependencies: dependencies{
		loadConfig: func(context.Context) (*config.Config, error) { return cfg, nil },
		resolvePaths: func() (config.Paths, error) {
			return config.PathsForConfigDir(filepath.Dir(filepath.Dir(cfg.Storage.Badger.Path))), nil
		},
		openStorage:    storage.OpenReadOnly,
		transports:     connectors.ProductionTransportRegistry,
		authentication: providerauth.ProductionRegistry,
	}}
}

func assertCheck(t *testing.T, report Report, id, status string) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			if check.Status != status {
				t.Fatalf("check %s status = %s, want %s: %#v", id, check.Status, status, check)
			}
			return check
		}
	}
	t.Fatalf("check %s is missing: %#v", id, report)
	return Check{}
}

func treeFingerprint(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[relative] = fmt.Sprintf("directory:%o", info.Mode().Perm())
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[relative] = fmt.Sprintf("file:%o:%x", info.Mode().Perm(), sha256.Sum256(data))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
