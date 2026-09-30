package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	legacyjson "encoding/json"
	"encoding/json/v2"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/agentstation/starmap/pkg/productpaths/policy"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/kvconnection"
	"github.com/agentstation/starport/internal/localauth"
)

// RecoverySelection contains private evidence for selected operator inputs.
// It binds settings and regular input files. It grants no admission authority.
type RecoverySelection struct{ state *recoverySelectionState }
type recoverySelectionState struct {
	body  []byte
	files int
}

// PrivateEvidence returns paths, native identities, and digests for private recovery storage.
// It contains no file contents or credential values.
func (s *RecoverySelection) PrivateEvidence() []byte {
	if s == nil || s.state == nil {
		return nil
	}
	return bytes.Clone(s.state.body)
}

// FileCount reports the number of selected regular inputs.
func (s *RecoverySelection) FileCount() int {
	if s == nil || s.state == nil {
		return 0
	}
	return s.state.files
}

// InspectRecoverySelection verifies current target inputs without changing them.
// External credential validity and remote identity availability remain normal runtime checks.
func (c *Config) InspectRecoverySelection(ctx context.Context) (*RecoverySelection, error) {
	if ctx == nil || c == nil {
		return nil, errors.New("recovery selection requires configuration and a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	selected := *c
	if err := selected.Validate(); err != nil {
		return nil, OperatorError(err)
	}
	if c.Storage.Badger.inMemory || c.Catalog.stateDirectoryScratch {
		return nil, errors.New("recovery selection requires persistent settings")
	}
	if c.Security.LocalTokenPath != c.EffectivePaths().LocalTokenFile {
		return nil, errors.New("recovery administrator token differs from its canonical selection")
	}
	if c.Identity.Enabled() {
		if err := c.Identity.RuntimeAcquisition().Validate(); err != nil {
			return nil, errors.New("recovery identity settings are incomplete")
		}
	}
	settings, err := c.recoverySettingsSHA256()
	if err != nil {
		return nil, err
	}
	manifest, err := c.FileManifest("")
	if err != nil {
		return nil, err
	}
	files, err := c.readSelectedRecoveryInputs(ctx, manifest)
	if err != nil {
		return nil, err
	}
	policyFiles, policyDirectory, err := c.inspectRecoveryInferencePolicy(ctx)
	if err != nil {
		return nil, err
	}
	files = append(files, policyFiles...)
	if _, err := c.LoadServerCertificate(ctx); err != nil {
		return nil, err
	}
	// Recheck each file after owner parsers read their selected inputs.
	for _, binding := range files {
		_, current, err := readRecoveryFile(ctx, productpaths.FileEntry{ID: binding.Role, Location: productpaths.Path{Path: binding.Path}, Policy: productpaths.FilePolicy{Access: binding.Access}})
		if err != nil || current != binding {
			return nil, errors.New("recovery selected input changed during validation")
		}
	}
	slices.SortFunc(files, func(a, b recoveryFileBinding) int { return strings.Compare(a.Role, b.Role) })
	body, err := json.Marshal(struct {
		Version                  int                   `json:"version"`
		Settings                 string                `json:"settings_sha256"`
		Files                    []recoveryFileBinding `json:"files"`
		InferencePolicyDirectory []string              `json:"inference_policy_directory"`
	}{1, settings, files, policyDirectory}, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	return &RecoverySelection{state: &recoverySelectionState{body: body, files: len(files)}}, nil
}

func (c *Config) recoverySettingsSHA256() (string, error) {
	material := make(map[string]string, len(c.Providers))
	sources := make(map[string]string, len(c.Providers))
	for _, entry := range c.Providers.Entries() {
		source, err := credentials.RecoveryMaterialSourceSHA256(entry.Config.CredentialSource)
		if err != nil {
			return "", errors.New("recovery selection requires an owner binding for a custom credential source")
		}
		sources[string(entry.ProviderID)] = source
		digest, err := entry.Config.Material.RecoverySelectionSHA256()
		if err != nil {
			return "", err
		}
		material[string(entry.ProviderID)] = digest
	}
	approvals, err := c.InferenceDestinationApprovals.RecoverySelectionSHA256()
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(struct {
		Settings  *Config           `json:"settings"`
		Paths     Paths             `json:"paths"`
		Catalog   map[string]string `json:"canonical_catalog"`
		Materials map[string]string `json:"provider_material_sha256"`
		Sources   map[string]string `json:"provider_source_sha256"`
		Approvals string            `json:"destination_approvals_sha256"`
		AuthFlag  bool              `json:"auth_mode_from_flag"`
	}{c, c.EffectivePaths(), c.Catalog.CatalogValues(), material, sources, approvals, c.authModeFromFlag}, json.Deterministic(true), legacyjson.FormatDurationAsNano(true))
	if err != nil {
		return "", errors.New("recovery settings could not be encoded")
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func (c *Config) inspectRecoveryInferencePolicy(ctx context.Context) ([]recoveryFileBinding, []string, error) {
	path := c.InferenceCredentialPolicyDirectory()
	paths := c.EffectivePaths()
	owner := credentials.SelectionPolicyOwner{Product: "starport", Deployment: paths.DeploymentID, Instance: paths.InstanceID}
	if err := credentials.InspectSelectionPolicyDirectory(ctx, path, owner); err != nil {
		return nil, nil, errors.New("recovery inference selection policy is absent or invalid")
	}
	directory, err := productfiles.ExistingDirectory(path)
	if err != nil {
		return nil, nil, err
	}
	identity, err := directory.Identity()
	if err != nil {
		return nil, nil, err
	}
	root, err := directory.Open()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = root.Close() }()
	listing, err := root.Open(".")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = listing.Close() }()
	entries, err := listing.ReadDir(4097)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, err
	}
	if len(entries) > 4096 {
		return nil, nil, errors.New("recovery inference selection policy exceeds its record bound")
	}
	files := []recoveryFileBinding{}
	for _, entry := range entries {
		if entry.Name() == productfiles.PublicationDirectoryName {
			continue
		}
		selected := productpaths.FileEntry{ID: InferenceCredentialPolicyRole + "/" + entry.Name(), Location: productpaths.Path{Path: filepath.Join(path, entry.Name())}, Policy: productpaths.FilePolicy{Access: policy.OwnerOnly}}
		_, binding, err := readRecoveryFile(ctx, selected)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, binding)
	}
	if err := credentials.InspectSelectionPolicyDirectory(ctx, path, owner); err != nil {
		return nil, nil, err
	}
	after, err := directory.Identity()
	if err != nil || after != identity {
		return nil, nil, errors.New("recovery inference policy directory changed")
	}
	return files, []string{path, identity}, nil
}

func (c *Config) readSelectedRecoveryInputs(ctx context.Context, manifest productpaths.FileManifest) ([]recoveryFileBinding, error) {
	files := []recoveryFileBinding{}
	for _, entry := range manifest.Files {
		selected := entry.ID == pathRoleConfiguration || strings.HasPrefix(entry.ID, "dotenv-") || slices.Contains([]string{fileRoleTLSCertificate, fileRoleTLSKey, cacheCAFileRole, valkeyCAFileRole, fileRoleLocalToken, fileRoleSourceFile}, entry.ID)
		if !selected || entry.Availability != fileAvailable {
			continue
		}
		if entry.ID == pathRoleConfiguration {
			absentDefault := false
			for _, input := range c.fileInputs {
				if input.primary && !input.loaded {
					absentDefault = true
				}
			}
			if absentDefault {
				if _, err := os.Lstat(entry.Location.Path); !errors.Is(err, os.ErrNotExist) {
					return nil, errors.New("recovery default configuration changed after loading")
				}
				continue
			}
		}
		body, binding, err := readRecoveryFile(ctx, entry)
		if err != nil {
			return nil, errors.New("recovery selected input could not be read: " + entry.ID)
		}
		for _, input := range c.fileInputs {
			if input.loaded && input.location.Path == entry.Location.Path && sha256.Sum256(body) != input.digest {
				return nil, errors.New("recovery configuration file differs from its loaded values")
			}
		}
		switch entry.ID {
		case fileRoleLocalToken:
			if err := localauth.CheckRecoveryToken(body, c.Server.Host); err != nil {
				return nil, errors.New("recovery administrator token is invalid or unsafe for this bind address")
			}
		case cacheCAFileRole, valkeyCAFileRole:
			if _, err := kvconnection.LoadRoots(entry.Location.Path); err != nil {
				return nil, err
			}
		}
		files = append(files, binding)
	}
	return files, nil
}
