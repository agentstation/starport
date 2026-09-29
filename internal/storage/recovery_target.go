package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"strconv"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/kvconnection"
)

// RecoveryTargetSHA256 binds the selected durable scope without credential material.
// Native import claims and external writer fencing remain mandatory.
func (c Config) RecoveryTargetSHA256(incarnation string) (string, error) {
	var scope []string
	switch c.Type {
	case StorageTypeBadger:
		if c.Badger.InMemory || incarnation != "" {
			return "", errors.New("recovery target requires persistent Badger without a remote incarnation")
		}
		directory, err := productfiles.ExistingDirectory(c.Badger.Path)
		if err != nil {
			return "", err
		}
		identity, err := directory.Identity()
		if err != nil {
			return "", err
		}
		scope = []string{"badger", c.Badger.Path, identity}
	case StorageTypeValkey:
		if err := c.Valkey.ValidateConnection(); err != nil {
			return "", err
		}
		if incarnation == "" || c.Valkey.DeploymentID == "" {
			return "", errors.New("recovery target requires a Valkey incarnation and deployment")
		}
		endpoint, _ := kvconnection.Parse(c.Valkey.URL, c.Valkey.AllowInsecure)
		database := c.Valkey.DB
		if endpoint.Path != "" {
			database, _ = strconv.Atoi(strings.TrimPrefix(endpoint.Path, "/"))
		}
		scope = []string{"valkey", endpoint.Host, strconv.Itoa(database), c.Valkey.DeploymentID, incarnation}
	default:
		return "", errors.New("recovery target requires native durable KV")
	}
	body, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}
