package config

import (
	"context"
	"crypto/tls"
	"errors"

	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/agentstation/starmap/pkg/productpaths/policy"
)

// LoadServerCertificate reads the selected TLS pair through native file access checks.
// Disabled TLS returns no certificate. Errors contain no paths or certificate bytes.
func (c *Config) LoadServerCertificate(ctx context.Context) (*tls.Certificate, error) {
	if ctx == nil || c == nil {
		return nil, errors.New("server TLS requires configuration and a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !c.Security.EnableTLS {
		return nil, nil
	}
	cert, _, err := readRecoveryFile(ctx, productpaths.FileEntry{ID: fileRoleTLSCertificate, Location: productpaths.Path{Path: c.Security.TLSCertPath}, Policy: productpaths.FilePolicy{Access: policy.DeploymentControlled}})
	if err != nil {
		return nil, errors.New("server TLS certificate could not be read")
	}
	key, _, err := readRecoveryFile(ctx, productpaths.FileEntry{ID: fileRoleTLSKey, Location: productpaths.Path{Path: c.Security.TLSKeyPath}, Policy: productpaths.FilePolicy{Access: policy.OwnerOnly}})
	if err != nil {
		return nil, errors.New("server TLS key could not be read")
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, errors.New("server TLS certificate and key are invalid or do not match")
	}
	return &pair, nil
}
