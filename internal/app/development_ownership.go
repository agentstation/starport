package app

import (
	"errors"
	"io"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
)

const developmentMetadataEntryLimit = 8

// metadataIdentities records the private files that session setup creates.
// Unknown additions or replacements must survive later cleanup.
func (s *developmentScratch) metadataIdentities() (map[string]string, error) {
	identities := make(map[string]string)
	if err := scratchMetadataIdentities(s.metadata, "", identities); err != nil {
		return nil, err
	}
	return identities, nil
}

func scratchMetadataIdentities(directory *productfiles.Directory, prefix string, identities map[string]string) (resultErr error) {
	root, err := directory.Open()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	listing, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := listing.ReadDir(developmentMetadataEntryLimit + 1)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return errors.Join(readErr, listing.Close())
	}
	if err := listing.Close(); err != nil {
		return err
	}
	if len(entries) > developmentMetadataEntryLimit {
		return errDevelopmentScratchChanged
	}
	for _, entry := range entries {
		if prefix == "" && entry.Name() == developmentScratchRecordName {
			continue
		}
		name := filepath.Join(prefix, entry.Name())
		if entry.IsDir() {
			if prefix != "" || entry.Name() != ".record-publications" {
				return errDevelopmentScratchChanged
			}
			child, err := directory.ExistingChild(entry.Name())
			if err != nil {
				return err
			}
			identities[name], err = child.Identity()
			if err != nil {
				return err
			}
			if err := scratchMetadataIdentities(child, name, identities); err != nil {
				return err
			}
			continue
		}
		if _, err := directory.ReadFile(entry.Name(), 0); err != nil {
			return err
		}
		file, err := root.Open(entry.Name())
		if err != nil {
			return err
		}
		identity, err := productfiles.FileIdentity(file)
		if err := errors.Join(err, file.Close()); err != nil {
			return err
		}
		identities[name] = identity
	}
	return nil
}
