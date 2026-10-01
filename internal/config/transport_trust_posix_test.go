//go:build !windows

package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadServerCertificateRefusesSharedPrivateKey(t *testing.T) {
	cfg, _ := serverCertificateFixture(t)
	require.NoError(t, os.Chmod(cfg.Security.TLSKeyPath, 0640))
	_, err := cfg.LoadServerCertificate(t.Context())
	require.ErrorContains(t, err, "key could not be read")
	info, err := os.Stat(cfg.Security.TLSKeyPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0640), info.Mode().Perm())
}
