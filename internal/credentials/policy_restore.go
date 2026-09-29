package credentials

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/agentstation/starmap/pkg/productfiles"
)

const (
	selectionPolicyRestoreBatch = 128
	selectionPolicyRestoreLimit = 100000
)

// InspectSelectionPolicyDirectory validates retained inference policy without creating or changing files.
// It refuses pending publications. The caller must fence writers and verify the complete inventory.
// A valid directory does not authorize credentials, restore a replica, or approve admission.
func InspectSelectionPolicyDirectory(ctx context.Context, path string, owner SelectionPolicyOwner) (resultErr error) {
	if ctx == nil {
		return errors.New("inference policy inspection requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if owner.Product != "starport" || owner.Deployment == "" || owner.Instance == "" {
		return errors.New("inference policy requires a complete Starport owner")
	}
	directory, err := productfiles.ExistingDirectory(path)
	if err != nil {
		return err
	}
	if err := directory.CheckNoPendingPublications(ctx); err != nil {
		return err
	}
	root, err := directory.Open()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	listing, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, listing.Close()) }()
	total, hasDefault := 0, false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, readErr := listing.ReadDir(selectionPolicyRestoreBatch)
		total += len(entries)
		if total > selectionPolicyRestoreLimit {
			return errors.New("inference policy inventory exceeds its entry limit")
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Name() == productfiles.PublicationDirectoryName && entry.IsDir() {
				continue
			}
			if !entry.Type().IsRegular() {
				return errors.New("inference policy inventory contains an unsupported entry")
			}
			body, err := directory.ReadFile(entry.Name(), selectionPolicyRecordLimit)
			if err != nil {
				return err
			}
			record, err := decodeSelectionPolicyRecord(body, owner)
			if err != nil {
				return err
			}
			if entry.Name() == "policy.json" {
				if record.Provider != "" {
					return errors.New("inference policy default contains a provider")
				}
				hasDefault = true
			} else if record.Provider == "" || entry.Name() != selectionProviderFile(record.Provider) || record.Policy != InferencePolicyCurrent {
				return errors.New("inference policy inventory contains an invalid provider record")
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read inference policy inventory: %w", readErr)
		}
	}
	if !hasDefault {
		return errors.New("inference policy default is absent")
	}
	if err := directory.CheckNoPendingPublications(ctx); err != nil {
		return err
	}
	if _, err := directory.Identity(); err != nil {
		return err
	}
	return ctx.Err()
}
