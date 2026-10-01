package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/recovery"
)

const recoveryActivationPreparation = "preparation.json"

// RecoveryActivationRequest is the recovery owner's operator input contract.
type RecoveryActivationRequest = recovery.ActivationRequest

type recoveryActivationPreparationRecord struct {
	Version   int                             `json:"version"`
	Request   RecoveryActivationRequest       `json:"request"`
	Transfer  catalog.TopologyTransferRequest `json:"transfer"`
	Operator  jsontext.Value                  `json:"operator"`
	Canonical jsontext.Value                  `json:"canonical"`
	Compiled  jsontext.Value                  `json:"compiled"`
}

type recoveryActivationRecord struct {
	Version         int                                 `json:"version"`
	Preparation     recoveryActivationPreparationRecord `json:"preparation"`
	History         jsontext.Value                      `json:"history"`
	Materialization jsontext.Value                      `json:"materialization"`
	Permission      jsontext.Value                      `json:"permission"`
}

func readActivationPreparation(ctx context.Context, directory *productfiles.Directory) (recoveryActivationPreparationRecord, []byte, error) {
	var record recoveryActivationPreparationRecord
	if err := directory.CheckNoPendingPublications(ctx); err != nil {
		return record, nil, err
	}
	body, err := directory.ReadFile(recoveryActivationPreparation, 24<<20)
	if err != nil {
		return record, nil, err
	}
	if json.Unmarshal(body, &record, json.RejectUnknownMembers(true)) != nil || record.Version != 1 {
		return record, nil, recovery.ErrConflict
	}
	canonical, err := json.Marshal(record, json.Deterministic(true))
	if err != nil || !bytes.Equal(body, canonical) {
		return record, nil, recovery.ErrConflict
	}
	return record, body, nil
}
func retainActivationPreparation(ctx context.Context, directory *productfiles.Directory, record recoveryActivationPreparationRecord) error {
	body, err := json.Marshal(record, json.Deterministic(true))
	if err != nil {
		return err
	}
	if len(body) > 24<<20 {
		return recovery.ErrConflict
	}
	prior, err := directory.ReadFile(recoveryActivationPreparation, 24<<20)
	if errors.Is(err, os.ErrNotExist) {
		prior = nil
	} else if err != nil {
		return err
	}
	if prior != nil && !bytes.Equal(prior, body) {
		return recovery.ErrConflict
	}
	return directory.CompareAndPublish(ctx, recoveryActivationPreparation, prior, body)
}

func canonicalRecordSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
