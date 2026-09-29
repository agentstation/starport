package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"net/url"

	"github.com/agentstation/starmap/pkg/productfiles"
)

// RecoveryTargetBinder identifies the selected scope without releasing import barriers.
// The digest supplements native claims and does not prove remote server continuity.
type RecoveryTargetBinder interface {
	RecoveryTargetSHA256() (string, error)
}

func (t filesystemRestoreTarget) RecoveryTargetSHA256() (string, error) {
	directory, err := productfiles.ExistingDirectory(t.destination)
	if err != nil {
		return "", err
	}
	identity, err := directory.Identity()
	if err != nil {
		return "", err
	}
	return recoveryScopeDigest([]string{"filesystem", t.destination, identity})
}

func (t objectRestoreTarget) RecoveryTargetSHA256() (string, error) {
	if t.store == nil || t.store.client == nil {
		return "", errors.New("blob: recovery target requires an object client")
	}
	options := t.store.client.Options()
	endpoint := ""
	if options.BaseEndpoint != nil {
		parsed, err := url.Parse(*options.BaseEndpoint)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" || parsed.Scheme != "https" && parsed.Scheme != "http" {
			return "", errors.New("blob: recovery endpoint requires a host without credentials or query values")
		}
		endpoint = parsed.String()
	}
	return recoveryScopeDigest([]string{"objectstore", endpoint, options.Region, t.store.bucket, t.store.prefix})
}

func recoveryScopeDigest(scope []string) (string, error) {
	body, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}
