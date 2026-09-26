package connection

import (
	"crypto/x509"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const maxTrustBundleBytes = 1 << 20

// LoadRoots loads an explicit trust bundle without changing system trust.
// The deployment owns the file's access policy. Errors expose no paths or contents.
func LoadRoots(path string) (*x509.CertPool, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("cache CA file requires an absolute resolved path")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("cache CA file must be a readable regular file")
	}
	file, err := os.OpenInRoot(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return nil, errors.New("cache CA file could not be opened")
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxTrustBundleBytes {
		return nil, errors.New("cache CA file exceeds the regular-file size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxTrustBundleBytes+1))
	if err != nil || len(data) > maxTrustBundleBytes {
		return nil, errors.New("cache CA file could not be read within its size limit")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(data) {
		return nil, errors.New("cache CA file contains no valid certificates")
	}
	return roots, nil
}
