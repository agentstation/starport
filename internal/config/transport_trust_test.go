package config

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/kvconnection/kvtest"
	"github.com/stretchr/testify/require"
)

func serverCertificateFixture(t *testing.T) (*Config, []byte) {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "tls")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	pair, _ := kvtest.Certificate(t, false)
	var cert []byte
	for _, der := range pair.Certificate {
		cert = append(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	key, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	require.NoError(t, err)
	key = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
	cfg := &Config{Security: SecurityConfig{EnableTLS: true, TLSCertPath: filepath.Join(parent, "certificate.pem"), TLSKeyPath: filepath.Join(parent, "key.pem")}}
	require.NoError(t, os.WriteFile(cfg.Security.TLSCertPath, cert, 0644))
	require.NoError(t, os.WriteFile(cfg.Security.TLSKeyPath, key, 0600))
	return cfg, key
}

func TestLoadServerCertificateUsesSelectedNativeFiles(t *testing.T) {
	cfg, key := serverCertificateFixture(t)
	parsed, err := cfg.LoadServerCertificate(t.Context())
	require.NoError(t, err)
	require.Len(t, parsed.Certificate, 2)
	require.NotNil(t, parsed.PrivateKey)
	after, err := os.ReadFile(cfg.Security.TLSKeyPath)
	require.NoError(t, err)
	require.Equal(t, key, after)
	other, _ := serverCertificateFixture(t)
	cfg.Security.TLSKeyPath = other.Security.TLSKeyPath
	_, err = cfg.LoadServerCertificate(t.Context())
	require.ErrorContains(t, err, "do not match")
	require.NotContains(t, err.Error(), "PRIVATE KEY")
	cfg.Security.EnableTLS = false
	parsed, err = cfg.LoadServerCertificate(t.Context())
	require.NoError(t, err)
	require.Nil(t, parsed)
	cfg.Security.EnableTLS = true
	require.NoError(t, os.WriteFile(cfg.Security.TLSKeyPath, []byte("invalid private fixture"), 0600))
	_, err = cfg.LoadServerCertificate(t.Context())
	require.Error(t, err)
	require.NotContains(t, err.Error(), "invalid private fixture")
}

func TestLoadServerCertificateRefusesInvalidContextAndBounds(t *testing.T) {
	cfg, _ := serverCertificateFixture(t)
	_, err := cfg.LoadServerCertificate(nil)
	require.Error(t, err)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = cfg.LoadServerCertificate(cancelled)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, os.WriteFile(cfg.Security.TLSCertPath, make([]byte, recoveryInputMaxBytes+1), 0644))
	_, err = cfg.LoadServerCertificate(t.Context())
	require.ErrorContains(t, err, "certificate could not be read")
}
