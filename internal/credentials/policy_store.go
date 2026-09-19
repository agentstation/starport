package credentials

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/pkg/productfiles"
)

const (
	selectionPolicyRecordLimit   = 4096
	selectionPolicyWriteTimeout  = 5 * time.Second
	selectionPolicyRetryInterval = 10 * time.Millisecond
)

// SelectionPolicyOwner binds inference policy history to one deployment instance.
type SelectionPolicyOwner struct {
	Product    string `json:"product"`
	Deployment string `json:"deployment"`
	Instance   string `json:"instance"`
}

type selectionPolicyRecord struct {
	Schema   int                  `json:"schema"`
	Owner    SelectionPolicyOwner `json:"owner"`
	Provider catalogs.ProviderID  `json:"provider,omitzero"`
	Policy   EnvironmentPolicy    `json:"policy"`
}

// FileSelectionPolicyStore retains immutable default and accepted provider policies.
type FileSelectionPolicyStore struct {
	directory *productfiles.Directory
	owner     SelectionPolicyOwner
}

// OpenSelectionPolicyStore opens private history without reading credential sources.
// A retained default overrides the installation hint.
func OpenSelectionPolicyStore(ctx context.Context, path string, owner SelectionPolicyOwner, legacy bool) (*FileSelectionPolicyStore, error) {
	if ctx == nil {
		return nil, fmt.Errorf("inference policy requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if owner.Product != "starport" || owner.Deployment == "" || owner.Instance == "" {
		return nil, fmt.Errorf("inference policy requires a complete Starport owner")
	}
	directory, err := productfiles.NewDirectory(path)
	if err != nil {
		return nil, err
	}
	if err := directory.RecoverPublications(ctx); err != nil {
		return nil, err
	}
	store := &FileSelectionPolicyStore{directory: directory, owner: owner}
	if _, err := store.read("policy.json", ""); err == nil {
		return store, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	initial := InferencePolicyCurrent
	if legacy {
		initial = InferencePolicyLegacy
	}
	if err := store.create(ctx, "policy.json", selectionPolicyRecord{Schema: 1, Owner: owner, Policy: initial}); err != nil {
		return nil, err
	}
	if _, err := store.read("policy.json", ""); err != nil {
		return nil, err
	}
	return store, nil
}

// Policy reads the installation default and any accepted provider migration.
func (s *FileSelectionPolicyStore) Policy(ctx context.Context, provider catalogs.ProviderID) (EnvironmentPolicy, error) {
	if ctx == nil || provider == "" {
		return "", fmt.Errorf("inference policy requires a context and provider")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	base, err := s.read("policy.json", "")
	if err != nil {
		return "", err
	}
	if base.Policy == InferencePolicyCurrent {
		return base.Policy, nil
	}
	record, err := s.read(selectionProviderFile(provider), provider)
	if os.IsNotExist(err) {
		return base.Policy, nil
	}
	if err != nil {
		return "", err
	}
	if record.Policy != InferencePolicyCurrent {
		return "", fmt.Errorf("provider policy must record accepted migration")
	}
	return record.Policy, nil
}

// Accept records a provider decision without storing source names or material.
func (s *FileSelectionPolicyStore) Accept(ctx context.Context, provider catalogs.ProviderID) error {
	policy, err := s.Policy(ctx, provider)
	if err != nil || policy == InferencePolicyCurrent {
		return err
	}
	if err := s.create(ctx, selectionProviderFile(provider), selectionPolicyRecord{Schema: 1, Owner: s.owner, Provider: provider, Policy: InferencePolicyCurrent}); err != nil {
		return err
	}
	policy, err = s.Policy(ctx, provider)
	if err != nil {
		return err
	}
	if policy != InferencePolicyCurrent {
		return fmt.Errorf("inference policy did not advance")
	}
	return nil
}

func (s *FileSelectionPolicyStore) read(name string, provider catalogs.ProviderID) (selectionPolicyRecord, error) {
	data, err := s.directory.ReadFile(name, selectionPolicyRecordLimit)
	if err != nil {
		return selectionPolicyRecord{}, err
	}
	var record selectionPolicyRecord
	if err := json.Unmarshal(data, &record, json.RejectUnknownMembers(true)); err != nil {
		return record, fmt.Errorf("invalid inference policy encoding")
	}
	if record.Schema != 1 || record.Owner != s.owner || record.Provider != provider || (record.Policy != InferencePolicyLegacy && record.Policy != InferencePolicyCurrent) {
		return record, fmt.Errorf("inference policy owner or version does not match")
	}
	encoded, err := json.Marshal(record)
	if err != nil || !bytes.Equal(encoded, data) {
		return record, fmt.Errorf("inference policy requires canonical encoding")
	}
	return record, nil
}

func (s *FileSelectionPolicyStore) create(ctx context.Context, name string, record selectionPolicyRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(data) > selectionPolicyRecordLimit {
		return fmt.Errorf("inference policy exceeds its size limit")
	}
	writeCtx, cancel := context.WithTimeout(ctx, selectionPolicyWriteTimeout)
	defer cancel()
	for {
		err = s.directory.CompareAndPublish(writeCtx, name, nil, data)
		if _, uncertain := errors.AsType[*starmaperrors.PublicationError](err); uncertain {
			return err
		}
		conflict, ok := errors.AsType[*starmaperrors.ConflictError](err)
		if !ok {
			return err
		}
		if conflict.Resource != "private record writer" {
			retained, readErr := s.read(name, record.Provider)
			if readErr != nil {
				return readErr
			}
			if retained == record {
				return nil
			}
			return err
		}
		timer := time.NewTimer(selectionPolicyRetryInterval)
		select {
		case <-writeCtx.Done():
			timer.Stop()
			return writeCtx.Err()
		case <-timer.C:
		}
	}
}

func selectionProviderFile(provider catalogs.ProviderID) string {
	digest := sha256.Sum256([]byte(provider))
	return "provider-" + hex.EncodeToString(digest[:]) + ".json"
}
