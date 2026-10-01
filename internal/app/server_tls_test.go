package app

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func configureTestServerTLS(t *testing.T, cfg *config.Config) {
	t.Helper()
	certificateSource := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := certificateSource.TLS.Certificates[0]
	certificateSource.Close()
	key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	require.NoError(t, err)
	parent := filepath.Join(t.TempDir(), "tls")
	_, err = productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	cfg.Security.EnableTLS = true
	cfg.Security.TLSCertPath = filepath.Join(parent, "server.crt")
	cfg.Security.TLSKeyPath = filepath.Join(parent, "server.key")
	require.NoError(t, os.WriteFile(cfg.Security.TLSCertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0o600))
	require.NoError(t, os.WriteFile(cfg.Security.TLSKeyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0o600))
}

func TestProductionCompositionPassesSelectedServerCertificate(t *testing.T) {
	cfg := validProductionConfig(t)
	configureTestServerTLS(t, cfg)
	factories := explicitTestFactories(t)
	original := factories.newServer
	var selected *server.Config
	factories.newServer = func(value *server.Config, dependencies server.Dependencies) (httpRuntime, error) {
		selected = value
		return original(value, dependencies)
	}
	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	require.NotNil(t, selected)
	require.NotNil(t, selected.TLSCertificate)
	require.NotEmpty(t, selected.TLSCertificate.Certificate)
	require.NotNil(t, selected.TLSCertificate.PrivateKey)
}

func TestProductionCompositionRefusesInvalidTLSBeforeOpeningStorage(t *testing.T) {
	cfg := validProductionConfig(t)
	configureTestServerTLS(t, cfg)
	require.NoError(t, os.WriteFile(cfg.Security.TLSKeyPath, []byte("invalid-private-key"), 0o600))
	factories := explicitTestFactories(t)
	opened := false
	factories.openStorage = func(storage.Config) (storage.KVStore, error) {
		opened = true
		return nil, nil
	}
	application, err := New(cfg, withRuntimeFactories(factories))
	require.Error(t, err)
	require.Nil(t, application)
	require.False(t, opened, "TLS validation must precede storage and setup side effects")
}
