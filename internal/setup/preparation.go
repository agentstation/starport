package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/google/uuid"
)

const setupJournalVersion = 1

const (
	phasePreparing        = "preparing"
	phasePrepared         = "prepared"
	phasePublished        = "published"
	phaseRollback         = "rollback"
	phaseRollbackVerified = "rollback-verified"
	phaseRemoving         = "removing"
	phaseRestoring        = "restoring"
)

type setupJournal struct {
	Version       int             `json:"version"`
	ID            string          `json:"id"`
	ConfigFile    string          `json:"config_file"`
	BadgerDir     string          `json:"badger_dir"`
	ConfigRoot    string          `json:"config_root"`
	DataRoot      string          `json:"data_root"`
	Stage         string          `json:"stage"`
	StageIdentity string          `json:"stage_identity"`
	Phase         string          `json:"phase"`
	Configuration []byte          `json:"configuration"`
	APIKeyID      string          `json:"api_key_id"`
	APIKeyName    string          `json:"api_key_name"`
	Receipt       setupReceipt    `json:"receipt"`
	ConfigEntry   setupFileRecord `json:"config_entry"`
}

type setupPreparation struct {
	service       *Service
	writer        *setupWriter
	configuration *productfiles.Directory
	data          *productfiles.Directory
	journal       setupJournal
	journalBytes  []byte
	secret        string
}

func (s *Service) prepareAcrossRoots(ctx context.Context, request Request) (_ *setupPreparation, resultErr error) {
	if err := s.validate(request); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	masterKey, err := s.generateMasterKey()
	if err != nil {
		return nil, fmt.Errorf("generate security master key: %w", err)
	}
	contents, err := localConfig(masterKey)
	if err != nil {
		return nil, err
	}
	configuration, err := createSetupDirectory(s.configurationDirectory())
	if err != nil {
		return nil, err
	}
	writer, err := acquireSetupWriter(ctx, configuration)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, writer.close())
		}
	}()
	if _, err := writer.directory.ReadFile(setupJournalFile, setupJournalLimit); !os.IsNotExist(err) {
		return nil, errors.Join(fmt.Errorf("%w: retained setup transaction requires recovery", ErrPartialState), err)
	}
	for _, path := range []string{s.paths.ConfigFile, s.paths.BadgerDir} {
		if exists, err := pathExists(path); err != nil || exists {
			return nil, errors.Join(ErrPartialState, err)
		}
	}
	if err := inspectEmptySetup(s.paths); err != nil {
		return nil, err
	}
	data, err := createSetupDirectory(s.databaseParent())
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	stageName := ".starport-init-" + id
	stage, err := data.CreateChild(stageName)
	if err != nil {
		return nil, err
	}
	prepared := &setupPreparation{service: s, writer: writer, configuration: configuration, data: data,
		journal: setupJournal{Version: setupJournalVersion, ID: id, ConfigFile: s.paths.ConfigFile,
			BadgerDir: s.paths.BadgerDir, Stage: stageName, Phase: phasePreparing, Configuration: contents,
			APIKeyName: request.APIKeyName}}
	prepared.journal.ConfigRoot, err = configuration.Identity()
	if err != nil {
		return nil, err
	}
	prepared.journal.DataRoot, err = data.Identity()
	if err != nil {
		return nil, err
	}
	prepared.journal.StageIdentity, err = stage.Identity()
	if err != nil {
		return nil, err
	}
	if err := prepared.syncData(); err != nil {
		return nil, err
	}
	if err := prepared.save(ctx); err != nil {
		return nil, err
	}
	if err := prepared.checkpoint(phasePreparing); err != nil {
		return nil, err
	}
	issued, err := s.createAPIKey(ctx, filepath.Join(s.databaseParent(), stageName), request.APIKeyName)
	if err != nil {
		return nil, err
	}
	prepared.secret = issued.Secret
	prepared.journal.APIKeyID = issued.APIKey.ID
	prepared.journal.Receipt, err = captureSetupReceipt(stage)
	if err != nil {
		return nil, err
	}
	stageRoot, err := stage.Open()
	if err != nil {
		return nil, err
	}
	if err := errors.Join(productfiles.SyncDirectory(stageRoot), stageRoot.Close()); err != nil {
		return nil, err
	}
	prepared.journal.Phase = phasePrepared
	if err := prepared.save(ctx); err != nil {
		return nil, err
	}
	if err := prepared.checkpoint(phasePrepared); err != nil {
		return nil, err
	}
	return prepared, nil
}

func (p *setupPreparation) publish(ctx context.Context) (Result, error) {
	if err := p.checkRoots(ctx); err != nil {
		return Result{}, err
	}
	stage, err := p.data.ExistingChild(p.journal.Stage)
	if err != nil {
		return Result{}, err
	}
	if err := verifySetupReceipt(stage, p.journal.Receipt); err != nil {
		return Result{}, err
	}
	root, err := p.data.Open()
	if err != nil {
		return Result{}, err
	}
	err = productfiles.PublishDirectory(root, p.journal.Stage, root, filepath.Base(p.journal.BadgerDir))
	if err := errors.Join(err, root.Close()); err != nil {
		return Result{}, err
	}
	if err := p.syncData(); err != nil {
		return Result{}, err
	}
	if err := p.checkpoint("data-published"); err != nil {
		return Result{}, err
	}
	if err := p.checkRoots(ctx); err != nil {
		return Result{}, err
	}
	result := Result{APIKeyName: p.journal.APIKeyName, ConfigFile: p.journal.ConfigFile,
		DataDir: p.service.paths.DataDir, APIKey: p.secret, apiKeyID: p.journal.APIKeyID}
	if err := p.configuration.CompareAndPublish(ctx, filepath.Base(p.journal.ConfigFile), nil, p.journal.Configuration); err != nil {
		return result, err
	}
	configurationRoot, err := p.configuration.Open()
	if err != nil {
		return result, err
	}
	p.journal.ConfigEntry, err = setupRecord(configurationRoot, filepath.Base(p.journal.ConfigFile))
	if err := errors.Join(err, configurationRoot.Close()); err != nil {
		return result, err
	}
	p.journal.Phase = phasePublished
	journal := p.journal
	result.recovery = &journal
	if err := p.save(ctx); err != nil {
		return result, err
	}
	if err := p.checkpoint("configuration-published"); err != nil {
		return result, err
	}
	if _, err := p.writer.directory.CompareAndRemove(ctx, setupJournalFile, p.journalBytes); err != nil {
		return result, err
	}
	return result, nil
}

func (p *setupPreparation) save(ctx context.Context) error {
	if err := p.checkRoots(ctx); err != nil {
		return err
	}
	encoded, err := json.Marshal(p.journal)
	if err != nil {
		return err
	}
	if err := p.writer.directory.CompareAndPublish(ctx, setupJournalFile, p.journalBytes, encoded); err != nil {
		return err
	}
	p.journalBytes = encoded
	return nil
}

func (p *setupPreparation) checkpoint(name string) error {
	if p.service.checkpoint != nil {
		return p.service.checkpoint(name)
	}
	return nil
}

func (p *setupPreparation) syncData() error {
	root, err := p.data.Open()
	if err != nil {
		return err
	}
	return errors.Join(productfiles.SyncDirectory(root), root.Close())
}

func createSetupDirectory(path string) (*productfiles.Directory, error) {
	anchor := path
	for {
		_, err := os.Lstat(anchor)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) || filepath.Dir(anchor) == anchor {
			return nil, err
		}
		anchor = filepath.Dir(anchor)
	}
	directory, err := productfiles.NewDirectory(path)
	if err != nil {
		return nil, err
	}
	for current := path; ; current = filepath.Dir(current) {
		root, err := os.OpenRoot(current)
		if err != nil {
			return nil, err
		}
		if err := errors.Join(productfiles.SyncDirectory(root), root.Close()); err != nil {
			return nil, err
		}
		if current == anchor {
			break
		}
	}
	return directory, nil
}
