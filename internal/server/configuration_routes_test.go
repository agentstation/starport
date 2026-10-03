package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/audit"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/configops"
	"github.com/agentstation/starport/internal/configrevision"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/server/dto"
	"github.com/agentstation/starport/internal/sqlstore"
)

const (
	configurationDeploymentID = "server-configuration-test"
	configurationRoute        = "/api/v1/admin/config"
	// configurationOrigin is the origin of the gateway that httptest requests reach.
	configurationOrigin = "http://example.com"
	sourceKeyEdit       = "catalog_source_api_key"
)

// configurationFixture is one deployment behind the configuration routes.
// Each start loads the configuration again, as a new gateway process does.
type configurationFixture struct {
	environment map[string]string
	file        string
	sqlitePath  string
	audit       *configurationAudit
	probe       configops.Probe
	cfg         *config.Config
	db          *sqlstore.DB
	store       *configrevision.Store
	operations  *countedConfigurationOperations
	server      *Server
	admin       string
}

// configurationAudit records the local audit entries of a fixture.
type configurationAudit struct {
	mu      sync.Mutex
	entries []string
}

func (a *configurationAudit) record(_ context.Context, actor, action, subject string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, actor+" "+action+" "+subject)
	return nil
}

func (a *configurationAudit) records() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.entries...)
}

// countedConfigurationOperations counts the operation calls, so a test proves
// that a refused request reached no operation.
type countedConfigurationOperations struct {
	*configops.Service
	mu    sync.Mutex
	calls int
}

func (o *countedConfigurationOperations) count() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++
}

func (o *countedConfigurationOperations) called() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
}

func (o *countedConfigurationOperations) Validate(ctx context.Context, request config.FieldSave) (config.Validation, error) {
	o.count()
	return o.Service.Validate(ctx, request)
}

func (o *countedConfigurationOperations) TestConnection(ctx context.Context, request config.FieldSave) (config.ConnectionResult, error) {
	o.count()
	return o.Service.TestConnection(ctx, request)
}

func (o *countedConfigurationOperations) Save(ctx context.Context, request config.FieldSave, actor string) (config.Receipt, error) {
	o.count()
	return o.Service.Save(ctx, request, actor)
}

// newLocalConfigurationFixture serves a configuration file in a private
// directory. The management mode is local or external.
func newLocalConfigurationFixture(t *testing.T, management, fileValues string) *configurationFixture {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "starport")
	require.NoError(t, os.MkdirAll(directory, 0o700))
	file := filepath.Join(directory, "starport.env")
	require.NoError(t, os.WriteFile(file, []byte(fileValues), 0o600))
	fixture := &configurationFixture{
		environment: map[string]string{
			"STARPORT_CONFIG_MANAGEMENT": management,
			"STARPORT_DEPLOYMENT_ID":     configurationDeploymentID,
			"STARPORT_CATALOG_STATE_DIR": filepath.Join(directory, "catalog-state"),
		},
		file: file, audit: &configurationAudit{},
	}
	fixture.start(t)
	return fixture
}

// newSharedConfigurationFixture serves a shared deployment whose revision 1
// holds values. Its credentials are sealed with the master key.
func newSharedConfigurationFixture(t *testing.T, values map[string]string) *configurationFixture {
	t.Helper()
	fixture := newLocalConfigurationFixture(t, config.ManagementLocal, "")
	fixture.sqlitePath = filepath.Join(t.TempDir(), "starport.db")
	maps.Copy(fixture.environment, map[string]string{
		"STARPORT_CONFIG_MANAGEMENT":       config.ManagementShared,
		"STARPORT_STORAGE_SQL_SQLITE_PATH": fixture.sqlitePath,
		"STARPORT_SECURITY_MASTER_KEY":     strings.Repeat("k", 32),
	})
	cfg := fixture.load(t)
	store := fixture.openStore(t, fixture.openDB(t), cfg)
	_, err := store.Initialize(t.Context(), values, "operator:test", "initialize-server")
	require.NoError(t, err)
	fixture.start(t)
	return fixture
}

func (f *configurationFixture) shared() bool {
	return f.sqlitePath != ""
}

func (f *configurationFixture) load(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.NewLoader().
		WithPaths(config.PathsForConfigDir(filepath.Dir(f.file))).
		WithEnvironment(f.environment).WithEnvFiles(f.file).Load(t.Context())
	require.NoError(t, err)
	return cfg
}

func (f *configurationFixture) openDB(t *testing.T) *sqlstore.DB {
	t.Helper()
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: f.sqlitePath}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Migrate(t.Context()))
	return db
}

func (f *configurationFixture) openStore(t *testing.T, db *sqlstore.DB, cfg *config.Config) *configrevision.Store {
	t.Helper()
	trail, err := audit.Open(db, 0)
	require.NoError(t, err)
	sealer, err := credentials.NewEncryptionService([]byte(f.environment["STARPORT_SECURITY_MASTER_KEY"]))
	require.NoError(t, err)
	store, err := configrevision.New(db, trail, sealer, cfg.EffectivePaths().DeploymentID, cfg.ConfigNamespace())
	require.NoError(t, err)
	return store
}

// start composes a new process: it loads the configuration, applies the
// shared head, and serves the configuration routes.
func (f *configurationFixture) start(t *testing.T) {
	t.Helper()
	f.cfg = f.load(t)
	options := configops.Options{Probe: f.probe, LocalAudit: f.audit.record}
	if f.shared() {
		f.db = f.openDB(t)
		f.store = f.openStore(t, f.db, f.cfg)
		current, err := f.store.Current(t.Context())
		require.NoError(t, err)
		require.NoError(t, f.cfg.ApplySharedRevision(current.Shared()))
		cfg, store := f.cfg, f.store
		options.Store = store
		options.Observe = func(ctx context.Context) error {
			head, err := store.Head(ctx)
			if err == nil {
				cfg.ObserveDesiredRevision(head.Sequence)
			}
			return err
		}
	}
	service, err := configops.New(f.cfg, options)
	require.NoError(t, err)
	f.operations = &countedConfigurationOperations{Service: service}
	f.server = newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withTestConfigurationOperations(f.operations))
	f.admin = createServerAPIKey(t, f.server, "configuration-admin", []string{"admin"})
}

// call sends one request with the admin key. Each prepare step can change it.
func (f *configurationFixture) call(t *testing.T, method, route string, body any, prepare ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	}
	request := httptest.NewRequest(method, configurationRoute+route, reader)
	request.Header.Set("Authorization", "Bearer "+f.admin)
	request.Header.Set("Content-Type", "application/json")
	for _, step := range prepare {
		step(request)
	}
	recorder := httptest.NewRecorder()
	f.server.Router().ServeHTTP(recorder, request)
	return recorder
}

// revision is the expected revision of a save at the current state.
func (f *configurationFixture) revision(t *testing.T) string {
	t.Helper()
	if f.shared() {
		head, err := f.store.Head(t.Context())
		require.NoError(t, err)
		return strconv.FormatInt(head.Sequence, 10)
	}
	// A local revision is the SHA-256 checksum of the file.
	data, err := os.ReadFile(f.file)
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (f *configurationFixture) save(operationID, expected string, edits map[string]*string) config.FieldSave {
	return config.FieldSave{OperationID: operationID, DeploymentID: configurationDeploymentID, ExpectedRevision: expected, Edits: edits}
}

func edit(key, value string) map[string]*string {
	return map[string]*string{key: &value}
}

func withOrigin(origin string) func(*http.Request) {
	return func(request *http.Request) { request.Header.Set("Origin", origin) }
}

func decodeConfiguration[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &value), recorder.Body.String())
	return value
}

// configurationRefusalBody is the body of a refused configuration operation.
type configurationRefusalBody struct {
	Error   dto.ErrorDetail `json:"error"`
	Refusal config.Refusal  `json:"refusal"`
	Receipt *config.Receipt `json:"receipt"`
}

func requireConfigurationStatus(t *testing.T, recorder *httptest.ResponseRecorder, status int) {
	t.Helper()
	require.Equal(t, status, recorder.Code, recorder.Body.String())
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
}

func requireConfigurationRefusal(t *testing.T, recorder *httptest.ResponseRecorder, status int, reason string) configurationRefusalBody {
	t.Helper()
	requireConfigurationStatus(t, recorder, status)
	body := decodeConfiguration[configurationRefusalBody](t, recorder)
	require.Equal(t, reason, body.Refusal.Reason)
	require.Equal(t, reason, body.Error.Code)
	return body
}

func requireFileContent(t *testing.T, file, content string) {
	t.Helper()
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, content, string(data))
}

// sharedSourceValues is a revision that selects the Starmap source with a
// sealed API key.
func sharedSourceValues(key string) map[string]string {
	values := map[string]string{
		catalogconfig.Source:    config.CatalogSourceStarmap,
		catalogconfig.SourceURL: "https://catalog.example/api/v1",
	}
	if key != "" {
		values[catalogconfig.SourceAPIKey] = key
	}
	return values
}

func TestAdminConfigSaveReportsStaleLocalRevision(t *testing.T) {
	const original = "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n"
	fixture := newLocalConfigurationFixture(t, config.ManagementLocal, original)
	loaded := fixture.revision(t)

	response := fixture.call(t, http.MethodPost, "/save", fixture.save("op-first", loaded, edit("catalog_acquisition_interval", "9m")))
	requireConfigurationStatus(t, response, http.StatusOK)
	saved := fixture.revision(t)
	require.NotEqual(t, loaded, saved)

	response = fixture.call(t, http.MethodPost, "/save", fixture.save("op-second", loaded, edit("catalog_acquisition_interval", "11m")))
	body := requireConfigurationRefusal(t, response, http.StatusConflict, config.RefusalStaleRevision)
	require.Equal(t, loaded, body.Refusal.Expected)
	require.Equal(t, saved, body.Refusal.Current)
	require.NotNil(t, body.Receipt)
	require.Equal(t, config.OperationRefused, body.Receipt.Status)
	require.Equal(t, "op-second", body.Receipt.OperationID)
	require.Equal(t, config.ManagementLocal, body.Receipt.Management)
	requireFileContent(t, fixture.file, "STARPORT_CATALOG_ACQUISITION_INTERVAL='9m'\n")

	// Validation reports the same refusal in its result.
	response = fixture.call(t, http.MethodPost, "/validate", fixture.save("", loaded, edit("catalog_acquisition_interval", "11m")))
	requireConfigurationStatus(t, response, http.StatusOK)
	validation := decodeConfiguration[config.Validation](t, response)
	require.False(t, validation.Valid)
	require.Equal(t, config.RefusalStaleRevision, validation.Refusal.Reason)

	response = fixture.call(t, http.MethodGet, "/operations/op-second", nil)
	requireConfigurationStatus(t, response, http.StatusNotFound)
}

func TestAdminConfigSaveReportsStaleSharedRevision(t *testing.T) {
	fixture := newSharedConfigurationFixture(t, sharedSourceValues(""))

	response := fixture.call(t, http.MethodPost, "/save", fixture.save("op-first", "1", edit(sourceKeyEdit, "first-source-key")))
	requireConfigurationStatus(t, response, http.StatusOK)
	require.Equal(t, int64(2), decodeConfiguration[config.Receipt](t, response).Saved.Sequence)

	response = fixture.call(t, http.MethodPost, "/save", fixture.save("op-second", "1", edit(sourceKeyEdit, "second-source-key")))
	body := requireConfigurationRefusal(t, response, http.StatusConflict, config.RefusalStaleRevision)
	require.Equal(t, "1", body.Refusal.Expected)
	require.Equal(t, "2", body.Refusal.Current)
	require.Equal(t, config.OperationRefused, body.Receipt.Status)
	require.Equal(t, config.ManagementShared, body.Receipt.Management)
	require.Equal(t, "2", fixture.revision(t))

	// An expected revision that is not the decimal head sequence refuses as invalid.
	response = fixture.call(t, http.MethodPost, "/save", fixture.save("op-third", "02", edit(sourceKeyEdit, "third-source-key")))
	requireConfigurationRefusal(t, response, http.StatusUnprocessableEntity, config.RefusalInvalidEdit)
	require.Equal(t, "2", fixture.revision(t))
}

func TestAdminConfigReceiptSeparatesSavedAndApplied(t *testing.T) {
	t.Run("shared", func(t *testing.T) {
		fixture := newSharedConfigurationFixture(t, sharedSourceValues(""))
		response := fixture.call(t, http.MethodPost, "/save", fixture.save("op-saved", "1", edit(sourceKeyEdit, "saved-source-key")))
		requireConfigurationStatus(t, response, http.StatusOK)
		receipt := decodeConfiguration[config.Receipt](t, response)
		require.Equal(t, config.OperationSaved, receipt.Status)
		require.Equal(t, int64(2), receipt.Saved.Sequence)
		require.Equal(t, int64(1), receipt.Applied.Sequence)
		require.NotEmpty(t, receipt.Saved.Checksum)
		require.Empty(t, receipt.ActivationError)
		require.Equal(t, []string{sourceKeyEdit}, receipt.Settings)
		require.Equal(t, "key:configuration_admin", receipt.Actor)
		require.Equal(t, configurationDeploymentID, receipt.DeploymentID)
		require.NotEmpty(t, receipt.CreatedAt)

		// The effective report shows the new desired revision and the applied one.
		effective := decodeConfiguration[config.EffectiveReport](t, fixture.call(t, http.MethodGet, "/effective", nil))
		require.Equal(t, int64(2), effective.Revision.Desired)
		require.Equal(t, int64(1), effective.Revision.Applied)
	})
	t.Run("local", func(t *testing.T) {
		fixture := newLocalConfigurationFixture(t, config.ManagementLocal, "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n")
		loaded := fixture.revision(t)
		response := fixture.call(t, http.MethodPost, "/save", fixture.save("op-saved", loaded, edit("catalog_acquisition_interval", "9m")))
		requireConfigurationStatus(t, response, http.StatusOK)
		receipt := decodeConfiguration[config.Receipt](t, response)
		require.Equal(t, config.OperationSaved, receipt.Status)
		require.Equal(t, config.ManagementLocal, receipt.Management)
		require.Equal(t, fixture.revision(t), receipt.Saved.Revision)
		require.Equal(t, loaded, receipt.Applied.Revision)
		require.Zero(t, receipt.Saved.Sequence)
		require.Equal(t, []string{"catalog_acquisition_interval"}, receipt.Settings)
	})
}

func TestAdminConfigOperationReceiptSurvivesRestart(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		fixture := newLocalConfigurationFixture(t, config.ManagementLocal, "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n")
		request := fixture.save("op-restart", fixture.revision(t), edit("catalog_acquisition_interval", "9m"))
		response := fixture.call(t, http.MethodPost, "/save", request)
		requireConfigurationStatus(t, response, http.StatusOK)
		original := decodeConfiguration[config.Receipt](t, response)

		fixture.start(t)
		response = fixture.call(t, http.MethodGet, "/operations/op-restart", nil)
		requireConfigurationStatus(t, response, http.StatusOK)
		restarted := decodeConfiguration[config.Receipt](t, response)
		require.Equal(t, config.OperationApplied, restarted.Status, "the new process serves the saved file")
		require.Equal(t, original.Saved, restarted.Saved)
		require.Equal(t, original.Saved, restarted.Applied)
		require.Equal(t, original.Settings, restarted.Settings)
		require.Equal(t, original.CreatedAt, restarted.CreatedAt)

		// An exact retry after the restart returns the receipt and writes nothing.
		response = fixture.call(t, http.MethodPost, "/save", request)
		requireConfigurationStatus(t, response, http.StatusOK)
		require.Equal(t, restarted, decodeConfiguration[config.Receipt](t, response))
		require.Len(t, fixture.audit.records(), 1)
	})
	t.Run("shared", func(t *testing.T) {
		fixture := newSharedConfigurationFixture(t, sharedSourceValues(""))
		request := fixture.save("op-restart", "1", edit(sourceKeyEdit, "restart-source-key"))
		response := fixture.call(t, http.MethodPost, "/save", request)
		requireConfigurationStatus(t, response, http.StatusOK)
		original := decodeConfiguration[config.Receipt](t, response)

		fixture.start(t)
		response = fixture.call(t, http.MethodGet, "/operations/op-restart", nil)
		requireConfigurationStatus(t, response, http.StatusOK)
		restarted := decodeConfiguration[config.Receipt](t, response)
		require.Equal(t, config.OperationApplied, restarted.Status)
		require.Equal(t, original.Saved, restarted.Saved)
		require.Equal(t, original.Saved, restarted.Applied)
		require.Equal(t, original.Settings, restarted.Settings)
		require.Equal(t, original.CreatedAt, restarted.CreatedAt)
		require.Equal(t, "restart-source-key", fixture.cfg.Catalog.SourceAPIKey)

		response = fixture.call(t, http.MethodPost, "/save", request)
		requireConfigurationStatus(t, response, http.StatusOK)
		require.Equal(t, restarted, decodeConfiguration[config.Receipt](t, response))
		require.Equal(t, "2", fixture.revision(t))
	})
	t.Run("unknown operation", func(t *testing.T) {
		fixture := newLocalConfigurationFixture(t, config.ManagementLocal, "")
		requireConfigurationStatus(t, fixture.call(t, http.MethodGet, "/operations/op-unknown", nil), http.StatusNotFound)
	})
}

// configurationRoutes are every configuration route with a valid body.
func configurationRoutes(fixture *configurationFixture, expected string) []struct {
	method, route string
	body          any
} {
	request := fixture.save("op-route", expected, edit("catalog_acquisition_interval", "9m"))
	return []struct {
		method, route string
		body          any
	}{
		{http.MethodGet, "/schema", nil},
		{http.MethodGet, "/effective", nil},
		{http.MethodPost, "/validate", request},
		{http.MethodPost, "/test-connection", request},
		{http.MethodPost, "/save", request},
		{http.MethodGet, "/operations/op-route", nil},
	}
}

func TestAdminConfigWritesRequireAdminScope(t *testing.T) {
	const original = "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n"
	fixture := newLocalConfigurationFixture(t, config.ManagementLocal, original)
	reader := createServerAPIKey(t, fixture.server, "configuration-reader", []string{"models:read", "catalog:read"})
	for _, route := range configurationRoutes(fixture, fixture.revision(t)) {
		t.Run(route.method+" "+route.route, func(t *testing.T) {
			response := fixture.call(t, route.method, route.route, route.body, func(request *http.Request) { request.Header.Del("Authorization") })
			require.Equal(t, http.StatusUnauthorized, response.Code)

			response = fixture.call(t, route.method, route.route, route.body, func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer "+reader)
			})
			require.Equal(t, http.StatusForbidden, response.Code)
			assertOpenRouterError(t, response, http.StatusForbidden, "permission_error")
		})
	}
	require.Zero(t, fixture.operations.called())
	requireFileContent(t, fixture.file, original)
	require.Empty(t, fixture.audit.records())

	// The admin scope reaches the routes.
	response := fixture.call(t, http.MethodPost, "/save", fixture.save("op-admin", fixture.revision(t), edit("catalog_acquisition_interval", "9m")))
	requireConfigurationStatus(t, response, http.StatusOK)
}

func TestAdminConfigSaveRefusesExternalController(t *testing.T) {
	const original = "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n"
	fixture := newLocalConfigurationFixture(t, config.ManagementExternal, original)
	request := fixture.save("op-external", fixture.revision(t), edit("catalog_acquisition_interval", "9m"))

	for _, route := range []string{"/save", "/validate", "/test-connection"} {
		response := fixture.call(t, http.MethodPost, route, request)
		body := requireConfigurationRefusal(t, response, http.StatusLocked, config.RefusalExternalManagement)
		if route == "/save" {
			require.Equal(t, config.ManagementExternal, body.Receipt.Management)
			require.Equal(t, config.OperationRefused, body.Receipt.Status)
		}
	}
	requireFileContent(t, fixture.file, original)
	require.NoFileExists(t, filepath.Join(filepath.Dir(fixture.file), ".starport-config-operations.json"))
	require.Empty(t, fixture.audit.records())

	// Reads continue and report the external controller.
	response := fixture.call(t, http.MethodGet, "/schema", nil)
	requireConfigurationStatus(t, response, http.StatusOK)
	effective := decodeConfiguration[config.EffectiveReport](t, fixture.call(t, http.MethodGet, "/effective", nil))
	require.Equal(t, config.ManagementExternal, effective.Management)
	require.Equal(t, config.ManagementExternal, effective.Controller)
	requireConfigurationStatus(t, fixture.call(t, http.MethodGet, "/operations/op-external", nil), http.StatusNotFound)
}

func TestAdminConfigWritesRefuseCrossSiteOrigin(t *testing.T) {
	const original = "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n"
	fixture := newLocalConfigurationFixture(t, config.ManagementLocal, original)
	token, err := localauth.Mint(1, time.Now())
	require.NoError(t, err)
	fixture.server.auth.AcceptSessions(localauth.NewGate(token, "127.0.0.1"))
	consoleSession := func(request *http.Request) {
		request.Header.Del("Authorization")
		request.AddCookie(&http.Cookie{Name: localauth.SessionCookie, Value: openSession(t, token)})
	}
	request := fixture.save("op-origin", fixture.revision(t), edit("catalog_acquisition_interval", "9m"))

	for _, route := range []string{"/save", "/validate", "/test-connection"} {
		t.Run(route, func(t *testing.T) {
			for name, prepare := range map[string][]func(*http.Request){
				"cross-site origin":              {withOrigin("https://attacker.example")},
				"cross-site fetch metadata":      {func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
				"console session without origin": {consoleSession},
				"console session cross-site":     {consoleSession, withOrigin("https://attacker.example")},
			} {
				response := fixture.call(t, http.MethodPost, route, request, prepare...)
				require.Equal(t, http.StatusForbidden, response.Code, name)
				body := decodeConfiguration[configurationRefusalBody](t, response)
				require.Equal(t, dto.ErrorTypePermissionError, body.Error.Type, name)
			}
		})
	}
	require.Zero(t, fixture.operations.called(), "a refused origin reaches no operation")
	requireFileContent(t, fixture.file, original)
	require.Empty(t, fixture.audit.records())

	// A console session write with the gateway's own origin succeeds.
	response := fixture.call(t, http.MethodPost, "/save", request, consoleSession, withOrigin(configurationOrigin))
	requireConfigurationStatus(t, response, http.StatusOK)
	receipt := decodeConfiguration[config.Receipt](t, response)
	require.Equal(t, config.OperationSaved, receipt.Status)
	require.True(t, strings.HasPrefix(receipt.Actor, "console:"), receipt.Actor)

	// An API key write with a same-origin header succeeds too.
	response = fixture.call(t, http.MethodPost, "/validate", fixture.save("", fixture.revision(t), edit("catalog_acquisition_interval", "11m")), withOrigin(configurationOrigin))
	requireConfigurationStatus(t, response, http.StatusOK)
	require.True(t, decodeConfiguration[config.Validation](t, response).Valid)
}

func TestAdminConfigResponsesRedactSensitiveValues(t *testing.T) {
	const stored, saved, refused = "stored-source-key-value", "saved-source-key-value", "refused-source-key-value"
	fixture := newSharedConfigurationFixture(t, sharedSourceValues(stored))
	fixture.probe = func(_ context.Context, catalog config.CatalogConfig) error {
		return errors.New("source refused " + catalog.SourceAPIKey + " at " + catalog.SourceURL)
	}
	fixture.start(t)
	require.Equal(t, stored, fixture.cfg.Catalog.SourceAPIKey)
	secrets := []string{stored, saved, refused}

	responses := map[string]*httptest.ResponseRecorder{
		"schema":          fixture.call(t, http.MethodGet, "/schema", nil),
		"effective":       fixture.call(t, http.MethodGet, "/effective", nil),
		"validate":        fixture.call(t, http.MethodPost, "/validate", fixture.save("", "1", edit(sourceKeyEdit, saved))),
		"test-connection": fixture.call(t, http.MethodPost, "/test-connection", fixture.save("", "1", edit(sourceKeyEdit, saved))),
		"save":            fixture.call(t, http.MethodPost, "/save", fixture.save("op-redact", "1", edit(sourceKeyEdit, saved))),
	}
	responses["receipt"] = fixture.call(t, http.MethodGet, "/operations/op-redact", nil)
	responses["stale save"] = fixture.call(t, http.MethodPost, "/save", fixture.save("op-refused", "1", edit(sourceKeyEdit, refused)))
	responses["invalid edit"] = fixture.call(t, http.MethodPost, "/save", fixture.save("op-invalid", "2", edit("catalog_source_url", "https://"+refused+"@catalog.example/{")))
	responses["unknown key"] = fixture.call(t, http.MethodPost, "/save", fixture.save("op-unknown", "2", edit(refused, "value")))
	for name, response := range responses {
		require.Less(t, response.Code, http.StatusInternalServerError, name)
		for _, secret := range secrets {
			require.NotContains(t, response.Body.String(), secret, name)
		}
	}
	requireConfigurationStatus(t, responses["save"], http.StatusOK)
	connection := decodeConfiguration[config.ConnectionResult](t, responses["test-connection"])
	require.False(t, connection.Reachable)
	require.NotEmpty(t, connection.Error)

	// A sealed value appears as a presence marker. The revision checksum identifies it.
	effective := decodeConfiguration[config.EffectiveReport](t, responses["effective"])
	for _, setting := range effective.Settings {
		if setting.Name == catalogconfig.SourceAPIKey {
			require.Equal(t, "<redacted>", setting.Value)
		}
	}
	require.NotEmpty(t, effective.Revision.Checksum)

	// Audit records carry setting names and revisions, never values.
	trail, err := audit.Open(fixture.db, 0)
	require.NoError(t, err)
	page, err := trail.List(t.Context(), audit.Query{Limit: audit.MaxListLimit})
	require.NoError(t, err)
	require.NotEmpty(t, page.Records)
	for _, record := range page.Records {
		for _, secret := range secrets {
			require.NotContains(t, record.Actor+" "+record.Action+" "+record.Subject+" "+record.RequestID, secret)
		}
	}
	revision, found, err := fixture.store.RevisionAt(t.Context(), 2)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, saved, revision.Values[catalogconfig.SourceAPIKey], "the store keeps the value that the responses omit")
}

func TestAdminConfigSaveRefusesMigration(t *testing.T) {
	fixture := newSharedConfigurationFixture(t, sharedSourceValues(""))

	// The management mode is migration-only.
	schema := decodeConfiguration[struct {
		Settings []config.SchemaSetting `json:"settings"`
	}](t, fixture.call(t, http.MethodGet, "/schema", nil))
	management := schema.Settings[len(schema.Settings)-1]
	require.Equal(t, "config_management", management.Key)
	require.Equal(t, config.MutabilityMigrationOnly, management.Mutability)
	for _, key := range []string{"config_management", "STARPORT_CONFIG_MANAGEMENT"} {
		response := fixture.call(t, http.MethodPost, "/save", fixture.save("op-management", "1", edit(key, config.ManagementLocal)))
		body := requireConfigurationRefusal(t, response, http.StatusUnprocessableEntity, config.RefusalMigrationBoundary)
		require.Contains(t, body.Refusal.Message, "starport config migrate")
	}

	// A policy change belongs to config apply.
	response := fixture.call(t, http.MethodPost, "/save", fixture.save("op-policy", "1", edit("catalog_acquisition_interval", "9m")))
	body := requireConfigurationRefusal(t, response, http.StatusUnprocessableEntity, config.RefusalPolicyChange)
	require.Contains(t, body.Refusal.Message, "starport config apply")

	// A store behind this binary refuses with a migrate instruction. The save does not migrate it.
	var applied int
	require.NoError(t, fixture.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied))
	_, err := fixture.db.ExecContext(t.Context(), `DELETE FROM schema_migrations WHERE name = (SELECT MAX(name) FROM schema_migrations)`)
	require.NoError(t, err)
	response = fixture.call(t, http.MethodPost, "/save", fixture.save("op-behind", "1", edit(sourceKeyEdit, "behind-source-key")))
	body = requireConfigurationRefusal(t, response, http.StatusServiceUnavailable, config.RefusalSchemaBehind)
	require.Contains(t, body.Refusal.Message, "migrate")
	var after int
	require.NoError(t, fixture.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&after))
	require.Equal(t, applied-1, after)
	require.Equal(t, "1", fixture.revision(t))
}

// effectiveSaveFacts is the part of the effective payload that the console
// reads to build a save. The test decodes the wire names, as the console does.
type effectiveSaveFacts struct {
	Management string `json:"management"`
	Revision   struct {
		Desired      int64  `json:"desired"`
		FileChecksum string `json:"file_checksum"`
	} `json:"revision"`
	Target struct {
		Kind        string `json:"kind"`
		Path        string `json:"path"`
		Unavailable string `json:"unavailable"`
	} `json:"target"`
	Paths struct {
		ConfigFile   string `json:"config_file"`
		DeploymentID string `json:"deployment_id"`
		Origins      map[string]struct {
			Path   string `json:"path"`
			Origin string `json:"origin"`
		} `json:"origins"`
	} `json:"paths"`
	Storage []struct {
		ID        string `json:"id"`
		Selection string `json:"selection"`
		Lifetime  string `json:"lifetime"`
	} `json:"storage"`
}

func TestAdminConfigEffectiveNamesTheSaveTarget(t *testing.T) {
	t.Run("local file", func(t *testing.T) {
		fixture := newLocalConfigurationFixture(t, config.ManagementLocal, "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n")
		facts := decodeConfiguration[effectiveSaveFacts](t, fixture.call(t, http.MethodGet, "/effective", nil))
		require.Equal(t, config.SaveTargetLocalFile, facts.Target.Kind)
		require.Equal(t, fixture.file, facts.Target.Path)
		require.Empty(t, facts.Target.Unavailable)
		require.Equal(t, configurationDeploymentID, facts.Paths.DeploymentID)
		require.Equal(t, fixture.revision(t), facts.Revision.FileChecksum)
		require.NotEmpty(t, facts.Paths.Origins["config"].Origin)
		require.ElementsMatch(t, []string{"kv", "sql", "blobs"}, []string{facts.Storage[0].ID, facts.Storage[1].ID, facts.Storage[2].ID})

		// A save built only from the payload writes the named file.
		save := config.FieldSave{OperationID: "op-from-payload", DeploymentID: facts.Paths.DeploymentID, ExpectedRevision: facts.Revision.FileChecksum, Edits: edit("catalog_acquisition_interval", "9m")}
		requireConfigurationStatus(t, fixture.call(t, http.MethodPost, "/save", save), http.StatusOK)
		requireFileContent(t, facts.Target.Path, "STARPORT_CATALOG_ACQUISITION_INTERVAL='9m'\n")
	})
	t.Run("shared revision", func(t *testing.T) {
		fixture := newSharedConfigurationFixture(t, sharedSourceValues(""))
		facts := decodeConfiguration[effectiveSaveFacts](t, fixture.call(t, http.MethodGet, "/effective", nil))
		require.Equal(t, config.SaveTargetSharedRevision, facts.Target.Kind)
		require.Empty(t, facts.Target.Path)
		require.Equal(t, int64(1), facts.Revision.Desired)

		save := config.FieldSave{OperationID: "op-from-payload", DeploymentID: facts.Paths.DeploymentID, ExpectedRevision: strconv.FormatInt(facts.Revision.Desired, 10), Edits: edit(sourceKeyEdit, "payload-source-key")}
		response := fixture.call(t, http.MethodPost, "/save", save)
		requireConfigurationStatus(t, response, http.StatusOK)
		require.Equal(t, int64(2), decodeConfiguration[config.Receipt](t, response).Saved.Sequence)
		require.NotContains(t, fixture.call(t, http.MethodGet, "/effective", nil).Body.String(), "payload-source-key")
	})
	t.Run("external controller", func(t *testing.T) {
		fixture := newLocalConfigurationFixture(t, config.ManagementExternal, "")
		facts := decodeConfiguration[effectiveSaveFacts](t, fixture.call(t, http.MethodGet, "/effective", nil))
		require.Equal(t, config.SaveTargetExternalController, facts.Target.Kind)
		require.Empty(t, facts.Target.Path)
	})
}
