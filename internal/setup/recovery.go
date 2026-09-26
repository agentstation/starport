package setup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/google/uuid"
)

const setupJournalLimit = 1 << 20

func (s *Service) recoverAcrossRoots(ctx context.Context) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	configuration, err := productfiles.ExistingDirectory(s.configurationDirectory())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	metadata, err := configuration.ExistingChild(setupMetadataDirectory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := metadata.RecoverPublications(ctx); err != nil {
		return err
	}
	writer, err := acquireSetupWriter(ctx, configuration)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, writer.close()) }()
	if err := configuration.RecoverPublications(ctx); err != nil {
		return err
	}
	contents, err := metadata.ReadFile(setupJournalFile, setupJournalLimit)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	journal, err := s.decodeSetupJournal(contents)
	if err != nil {
		return err
	}
	data, err := productfiles.ExistingDirectory(s.databaseParent())
	if err != nil {
		return err
	}
	prepared := &setupPreparation{service: s, writer: writer, configuration: configuration, data: data,
		journal: journal, journalBytes: contents}
	if err := prepared.checkRoots(ctx); err != nil {
		return err
	}
	switch journal.Phase {
	case phasePreparing:
		return prepared.recoverEmptyPreparation(ctx)
	case phasePrepared, phasePublished:
		configuration, err := prepared.configuration.ReadFile(filepath.Base(journal.ConfigFile), setupJournalLimit)
		if err == nil {
			if !bytes.Equal(configuration, journal.Configuration) {
				return fmt.Errorf("%w: configuration changed after setup publication", ErrPartialState)
			}
			if _, err := data.ExistingChild(journal.Stage); !os.IsNotExist(err) {
				return errors.Join(ErrPartialState, err)
			}
			if journal.Phase == phasePublished {
				if err := prepared.checkConfiguration(); err != nil {
					return err
				}
			}
			installed, err := data.ExistingChild(filepath.Base(journal.BadgerDir))
			if err != nil {
				return err
			}
			identity, err := installed.Identity()
			if err != nil || identity != journal.StageIdentity {
				return errors.Join(ErrPartialState, err)
			}
			return prepared.clearJournal(ctx)
		}
		if !os.IsNotExist(err) {
			return err
		}
		return prepared.discardUnpublished(ctx)
	case phaseRollback, phaseRollbackVerified, phaseRemoving:
		return prepared.continueRollback(ctx)
	case phaseRestoring:
		return prepared.restoreDatabase(ctx)
	default:
		return ErrPartialState
	}
}

func (s *Service) decodeSetupJournal(contents []byte) (setupJournal, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var journal setupJournal
	if err := decoder.Decode(&journal); err != nil {
		return journal, fmt.Errorf("%w: invalid setup transaction: %w", ErrPartialState, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return journal, fmt.Errorf("%w: setup transaction contains trailing data", ErrPartialState)
	}
	id, err := uuid.Parse(journal.ID)
	if err != nil || id.String() != journal.ID || journal.Version != setupJournalVersion ||
		journal.ConfigFile != s.paths.ConfigFile || journal.BadgerDir != s.paths.BadgerDir ||
		journal.Stage != ".starport-init-"+journal.ID || journal.ConfigRoot == "" || journal.DataRoot == "" ||
		journal.StageIdentity == "" || len(journal.Configuration) == 0 || len(journal.Configuration) > setupJournalLimit {
		return journal, fmt.Errorf("%w: setup transaction identity or paths do not match", ErrPartialState)
	}
	if journal.Phase != phasePreparing {
		if journal.APIKeyID == "" || journal.Receipt.Directory != journal.StageIdentity ||
			len(journal.Receipt.Files) == 0 || len(journal.Receipt.Files) > setupFileCount {
			return journal, fmt.Errorf("%w: setup transaction lacks a complete database receipt", ErrPartialState)
		}
		var total int64
		for name, file := range journal.Receipt.Files {
			if !setupChildName(name) || !validSetupFileRecord(file) {
				return journal, fmt.Errorf("%w: invalid database file receipt", ErrPartialState)
			}
			total += file.Size
			if total > setupTreeLimit {
				return journal, fmt.Errorf("%w: setup transaction exceeds the database byte limit", ErrPartialState)
			}
		}
	}
	switch journal.Phase {
	case phasePreparing, phasePrepared:
	case phasePublished, phaseRollback, phaseRollbackVerified, phaseRestoring, phaseRemoving:
		if journal.Phase != phaseRemoving && !validSetupConfiguration(journal) {
			return journal, fmt.Errorf("%w: setup transaction lacks a configuration receipt", ErrPartialState)
		}
	default:
		return journal, fmt.Errorf("%w: unknown setup transaction phase", ErrPartialState)
	}
	return journal, nil
}

func validSetupFileRecord(record setupFileRecord) bool {
	digest, err := hex.DecodeString(record.Digest)
	return record.Identity != "" && record.Size >= 0 && record.Size <= setupFileLimit && err == nil && len(digest) == sha256.Size
}

func validSetupConfiguration(journal setupJournal) bool {
	digest := sha256.Sum256(journal.Configuration)
	return validSetupFileRecord(journal.ConfigEntry) && journal.ConfigEntry.Size == int64(len(journal.Configuration)) &&
		journal.ConfigEntry.Digest == hex.EncodeToString(digest[:])
}

func setupChildName(name string) bool {
	return filepath.IsLocal(name) && name != "." && !strings.ContainsAny(name, "/\\:\x00") && strings.TrimRight(name, " .") == name
}

func (p *setupPreparation) checkRoots(ctx context.Context) error {
	if err := p.writer.check(ctx); err != nil {
		return err
	}
	configuration, err := p.configuration.Identity()
	if err != nil || configuration != p.journal.ConfigRoot {
		return errors.Join(ErrPartialState, err)
	}
	data, err := p.data.Identity()
	if err != nil || data != p.journal.DataRoot {
		return errors.Join(ErrPartialState, err)
	}
	return nil
}

func (p *setupPreparation) clearJournal(ctx context.Context) error {
	if err := p.checkRoots(ctx); err != nil {
		return err
	}
	_, err := p.writer.directory.CompareAndRemove(ctx, setupJournalFile, p.journalBytes)
	return err
}

func (p *setupPreparation) recoverEmptyPreparation(ctx context.Context) error {
	if err := p.requireUnpublished(); err != nil {
		return err
	}
	stage, err := p.data.ExistingChild(p.journal.Stage)
	if os.IsNotExist(err) {
		return p.clearJournal(ctx)
	}
	if err != nil {
		return err
	}
	identity, err := stage.Identity()
	if err != nil || identity != p.journal.StageIdentity {
		return errors.Join(ErrPartialState, err)
	}
	if err := p.checkRoots(ctx); err != nil {
		return err
	}
	root, err := p.data.Open()
	if err != nil {
		return err
	}
	removeErr := root.Remove(p.journal.Stage)
	err = errors.Join(removeErr, productfiles.SyncDirectory(root), root.Close())
	if err != nil {
		return fmt.Errorf("%w: preserve incomplete setup database at %q: %w", ErrPartialState,
			filepath.Join(p.service.databaseParent(), p.journal.Stage), err)
	}
	return p.clearJournal(ctx)
}

func (p *setupPreparation) requireUnpublished() error {
	for _, path := range []string{p.journal.ConfigFile, p.journal.BadgerDir} {
		if exists, err := pathExists(path); err != nil || exists {
			return errors.Join(fmt.Errorf("%w: setup publication conflicts with recovery", ErrPartialState), err)
		}
	}
	return nil
}
