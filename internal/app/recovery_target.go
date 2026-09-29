package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/sqlstore"
)

func configuredRecoveryTarget(ctx context.Context, cfg *config.Config, db *sqlstore.DB, blobs blob.RestoreTarget, incarnation string) (string, error) {
	if cfg == nil {
		return "", errors.New("recovery target requires configuration")
	}
	kv, err := cfg.RuntimeStorage().RecoveryTargetSHA256(incarnation)
	if err != nil {
		return "", err
	}
	relational, err := db.RecoveryTargetSHA256(ctx, cfg.Storage.RuntimeSQL())
	if err != nil {
		return "", err
	}
	binder, ok := blobs.(blob.RecoveryTargetBinder)
	if !ok {
		return "", errors.New("recovery target requires a bound blob scope")
	}
	assets, err := binder.RecoveryTargetSHA256()
	if err != nil {
		return "", err
	}
	body, err := json.Marshal([]string{"starport-recovery-target-v1", cfg.EffectivePaths().DeploymentID, kv, relational, assets})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}
