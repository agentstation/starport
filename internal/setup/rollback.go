package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
)

func (s *Service) rollbackAcrossRoots(ctx context.Context, result Result) (resultErr error) {
	if s == nil || result.recovery == nil || result.ConfigFile != s.paths.ConfigFile || result.DataDir != s.paths.DataDir ||
		result.apiKeyID == "" || result.apiKeyID != result.recovery.APIKeyID {
		return ErrRollbackRefused
	}
	configuration, err := productfiles.ExistingDirectory(s.configurationDirectory())
	if err != nil {
		return errors.Join(ErrRollbackRefused, err)
	}
	data, err := productfiles.ExistingDirectory(s.databaseParent())
	if err != nil {
		return errors.Join(ErrRollbackRefused, err)
	}
	writer, err := acquireSetupWriter(ctx, configuration)
	if err != nil {
		return errors.Join(ErrRollbackRefused, err)
	}
	defer func() { resultErr = errors.Join(resultErr, writer.close()) }()
	prepared := &setupPreparation{service: s, writer: writer, configuration: configuration, data: data, journal: *result.recovery}
	if err := prepared.checkRoots(ctx); err != nil {
		return errors.Join(ErrRollbackRefused, err)
	}
	if err := prepared.checkConfiguration(); err != nil {
		return errors.Join(ErrRollbackRefused, err)
	}
	if err := prepared.checkRollbackLayout(); err != nil {
		return errors.Join(ErrRollbackRefused, err)
	}
	installed, err := data.ExistingChild(filepath.Base(prepared.journal.BadgerDir))
	if err != nil {
		return errors.Join(ErrRollbackRefused, err)
	}
	if err := verifySetupReceipt(installed, prepared.journal.Receipt); err != nil {
		return errors.Join(ErrRollbackRefused, err)
	}
	current, err := writer.directory.ReadFile(setupJournalFile, setupJournalLimit)
	if err == nil {
		journal, err := s.decodeSetupJournal(current)
		if err != nil || journal.ID != prepared.journal.ID {
			return errors.Join(ErrRollbackRefused, err)
		}
		prepared.journalBytes = current
	} else if !os.IsNotExist(err) {
		return errors.Join(ErrRollbackRefused, err)
	}
	prepared.journal.Phase = phaseRollback
	if err := prepared.save(ctx); err != nil {
		return errors.Join(ErrRollbackRefused, err)
	}
	if err := prepared.continueRollback(ctx); err != nil {
		return errors.Join(ErrRollbackRefused, err)
	}
	return nil
}

func (p *setupPreparation) checkConfiguration() (resultErr error) {
	root, err := p.configuration.Open()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	current, err := setupRecord(root, filepath.Base(p.journal.ConfigFile))
	if err != nil {
		return err
	}
	if current != p.journal.ConfigEntry {
		return fmt.Errorf("%w: configuration changed after initialization", ErrRollbackRefused)
	}
	return nil
}

func (p *setupPreparation) checkRollbackLayout() error {
	paths := p.service.paths
	for _, selected := range []struct {
		directory *productfiles.Directory
		path      string
	}{
		{p.configuration, p.service.configurationDirectory()}, {p.data, p.service.databaseParent()},
	} {
		children, err := setupEntries(selected.directory)
		if err != nil {
			return err
		}
		allowed := map[string]bool{}
		for _, managed := range []string{paths.ConfigFile, paths.BadgerDir,
			filepath.Join(p.service.databaseParent(), databaseLockDirectory(paths)),
			filepath.Join(p.service.configurationDirectory(), setupMetadataDirectory), filepath.Join(p.service.configurationDirectory(), recordPublicationDirectory)} {
			relative, err := filepath.Rel(selected.path, managed)
			if err != nil || !filepath.IsLocal(relative) || relative == "." {
				continue
			}
			for filepath.Dir(relative) != "." {
				relative = filepath.Dir(relative)
			}
			allowed[relative] = true
		}
		for _, child := range children {
			if !allowed[child.Name()] {
				return fmt.Errorf("%w: managed directory contains application state", ErrRollbackRefused)
			}
		}
	}
	return nil
}

func (p *setupPreparation) isolateDatabase(ctx context.Context) (*productfiles.Directory, error) {
	if err := p.checkRoots(ctx); err != nil {
		return nil, err
	}
	stage, stageErr := p.data.ExistingChild(p.journal.Stage)
	installed, installedErr := p.data.ExistingChild(filepath.Base(p.journal.BadgerDir))
	if stageErr == nil && os.IsNotExist(installedErr) {
		return stage, verifySetupReceipt(stage, p.journal.Receipt)
	}
	if !os.IsNotExist(stageErr) || installedErr != nil {
		return nil, errors.Join(ErrPartialState, stageErr, installedErr)
	}
	if err := verifySetupReceipt(installed, p.journal.Receipt); err != nil {
		return nil, err
	}
	root, err := p.data.Open()
	if err != nil {
		return nil, err
	}
	err = productfiles.PublishDirectory(root, filepath.Base(p.journal.BadgerDir), root, p.journal.Stage)
	err = errors.Join(err, productfiles.SyncDirectory(root), root.Close())
	if err != nil {
		return nil, err
	}
	if err := p.checkpoint("rollback-isolated"); err != nil {
		return nil, err
	}
	stage, err = p.data.ExistingChild(p.journal.Stage)
	if err != nil {
		return nil, err
	}
	return stage, verifySetupReceipt(stage, p.journal.Receipt)
}

func (p *setupPreparation) discardUnpublished(ctx context.Context) error {
	stage, err := p.isolateDatabase(ctx)
	if err != nil {
		return err
	}
	if err := p.requireUnpublished(); err != nil {
		return err
	}
	if err := p.service.validateRollbackRecords(ctx, filepath.Join(p.service.databaseParent(), p.journal.Stage), p.journal.APIKeyID); err != nil {
		return err
	}
	p.journal.Receipt, err = captureSetupReceipt(stage)
	if err != nil {
		return err
	}
	p.journal.Phase = phaseRemoving
	if err := p.save(ctx); err != nil {
		return err
	}
	return p.removeStage(ctx)
}

func (p *setupPreparation) continueRollback(ctx context.Context) (resultErr error) {
	defer func() {
		if resultErr == nil || (p.journal.Phase != phaseRollback && p.journal.Phase != phaseRollbackVerified) {
			return
		}
		if exists, err := pathExists(p.journal.ConfigFile); err != nil || !exists {
			return
		}
		// A refused rollback must leave the database at its configured path.
		resultErr = errors.Join(resultErr, p.restoreDatabase(context.WithoutCancel(ctx)))
	}()
	if p.journal.Phase == phaseRollback {
		if err := p.checkConfiguration(); err != nil {
			return err
		}
		stage, err := p.isolateDatabase(ctx)
		if err != nil {
			return err
		}
		if err := p.service.validateRollbackRecords(ctx, filepath.Join(p.service.databaseParent(), p.journal.Stage), p.journal.APIKeyID); err != nil {
			return err
		}
		p.journal.Receipt, err = captureSetupReceipt(stage)
		if err != nil {
			return err
		}
		p.journal.Phase = phaseRollbackVerified
		if err := p.save(ctx); err != nil {
			return err
		}
		if err := p.checkpoint(phaseRollbackVerified); err != nil {
			return err
		}
	}
	if p.journal.Phase == phaseRollbackVerified {
		if err := p.checkRoots(ctx); err != nil {
			return err
		}
		if exists, err := pathExists(p.journal.BadgerDir); err != nil || exists {
			return errors.Join(ErrPartialState, err)
		}
		stage, err := p.data.ExistingChild(p.journal.Stage)
		if err != nil {
			return err
		}
		if err := verifySetupReceipt(stage, p.journal.Receipt); err != nil {
			return err
		}
		if err := p.checkConfiguration(); err != nil && !os.IsNotExist(err) {
			return err
		}
		if _, err := p.configuration.CompareAndRemove(ctx, filepath.Base(p.journal.ConfigFile), p.journal.Configuration); err != nil {
			return err
		}
		p.journal.Phase = phaseRemoving
		if err := p.save(ctx); err != nil {
			return err
		}
		if err := p.checkpoint("rollback-config-removed"); err != nil {
			return err
		}
	}
	return p.removeStage(ctx)
}

func (p *setupPreparation) removeStage(ctx context.Context) (resultErr error) {
	if err := p.checkRoots(ctx); err != nil {
		return err
	}
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
	root, err := stage.Open()
	if err != nil {
		return err
	}
	defer func() {
		if root != nil {
			resultErr = errors.Join(resultErr, root.Close())
		}
	}()
	names, err := remainingSetupFiles(root, p.journal.Receipt)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := p.checkRoots(ctx); err != nil {
			return err
		}
		if err := p.checkStage(stage); err != nil {
			return err
		}
		actual, err := setupRecord(root, name)
		if err != nil || actual != p.journal.Receipt.Files[name] {
			return errors.Join(ErrPartialState, err)
		}
		if err := root.Remove(name); err != nil {
			return err
		}
		if err := productfiles.SyncDirectory(root); err != nil {
			return err
		}
		if err := p.checkpoint("rollback-file-removed"); err != nil {
			return err
		}
	}
	closeErr := root.Close()
	root = nil
	if closeErr != nil {
		return closeErr
	}
	if err := p.checkRoots(ctx); err != nil {
		return err
	}
	if err := p.checkStage(stage); err != nil {
		return err
	}
	parent, err := p.data.Open()
	if err != nil {
		return err
	}
	err = parent.Remove(p.journal.Stage)
	if err := errors.Join(err, productfiles.SyncDirectory(parent), parent.Close()); err != nil {
		return err
	}
	if err := p.checkpoint("rollback-directory-removed"); err != nil {
		return err
	}
	return p.clearJournal(ctx)
}

func (p *setupPreparation) checkStage(stage *productfiles.Directory) error {
	identity, err := stage.Identity()
	if err != nil || identity != p.journal.StageIdentity {
		return errors.Join(ErrPartialState, err)
	}
	return nil
}

func (p *setupPreparation) restoreDatabase(ctx context.Context) error {
	if err := p.checkRoots(ctx); err != nil {
		return err
	}
	if p.journal.Phase != phaseRestoring {
		p.journal.Phase = phaseRestoring
		if err := p.save(ctx); err != nil {
			return err
		}
	}
	stage, stageErr := p.data.ExistingChild(p.journal.Stage)
	installed, installedErr := p.data.ExistingChild(filepath.Base(p.journal.BadgerDir))
	if os.IsNotExist(stageErr) && installedErr == nil {
		if err := p.checkStage(installed); err != nil {
			return err
		}
		return p.clearJournal(ctx)
	}
	if stageErr != nil || !os.IsNotExist(installedErr) {
		return errors.Join(ErrPartialState, stageErr, installedErr)
	}
	if err := p.checkStage(stage); err != nil {
		return err
	}
	root, err := p.data.Open()
	if err != nil {
		return err
	}
	err = productfiles.PublishDirectory(root, p.journal.Stage, root, filepath.Base(p.journal.BadgerDir))
	if err := errors.Join(err, productfiles.SyncDirectory(root), root.Close()); err != nil {
		return err
	}
	if err := p.checkpoint("rollback-restored"); err != nil {
		return err
	}
	return p.clearJournal(ctx)
}
