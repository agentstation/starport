package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/sethvargo/go-envconfig"
)

// sharedRevisionLayer names the shared layer in a catalog resolution.
const sharedRevisionLayer = "shared-revision"

// SharedRevision is one stored deployment configuration revision. Values maps
// canonical Starmap setting names to values and can hold source credentials.
type SharedRevision struct {
	DeploymentID string
	Namespace    string
	Sequence     int64
	RevisionID   string
	Checksum     string
	Values       map[string]string
}

// AppliedRevision reports the configuration authority of this process.
// Desired is the newest stored sequence that this process observed. Applied is
// the sequence that it serves. Retained reports that the last observation
// failed and the process keeps its applied revision as a cache.
type AppliedRevision struct {
	Authority string `json:"authority"`
	Namespace string `json:"namespace,omitempty"`
	Desired   int64  `json:"desired,omitempty"`
	Applied   int64  `json:"applied,omitempty"`
	Checksum  string `json:"checksum,omitempty"`
	Retained  bool   `json:"retained"`
}

// NamespaceSetting labels the shared configuration namespace in an
// *AuthorityMismatchError. STARPORT_DEPLOYMENT_ID derives the namespace.
const NamespaceSetting = "shared configuration namespace"

// AuthorityMismatchError reports a bootstrap identity that differs from the
// stored shared configuration head. It names the stored value. DeploymentID,
// when set, names the deployment of the stored head.
type AuthorityMismatchError struct {
	Setting      string
	Configured   string
	Stored       string
	DeploymentID string
}

func (e *AuthorityMismatchError) Error() string {
	if e.DeploymentID != "" {
		return fmt.Sprintf("%s %q differs from the value %q that the stored shared configuration of deployment %q names", e.Setting, e.Configured, e.Stored, e.DeploymentID)
	}
	return fmt.Sprintf("%s %q differs from the stored shared configuration value %q", e.Setting, e.Configured, e.Stored)
}

// SharedSettingError reports a setting that a shared revision cannot supply.
type SharedSettingError struct {
	Name   string
	Reason string
}

func (e *SharedSettingError) Error() string {
	return fmt.Sprintf("shared configuration revision cannot supply %s: %s", e.Name, e.Reason)
}

// localCatalogResolution keeps the local catalog layers for a later shared
// resolution and the operator origin of each layer.
type localCatalogResolution struct {
	layers     []catalogconfig.Layer
	origins    map[string]string
	resolution catalogconfig.Resolution
}

func (s catalogSettingsLookuper) local() *localCatalogResolution {
	origins := make(map[string]string, len(s.layers))
	for _, layer := range s.layers {
		origins[layer.Name] = catalogPathOrigin(layer.Name, s)
	}
	return &localCatalogResolution{layers: slices.Clone(s.layers), origins: origins, resolution: s.resolution}
}

// authorityState is the applied shared revision. Copies of a Config share it.
type authorityState struct {
	mu         sync.RWMutex
	revision   AppliedRevision
	resolution catalogconfig.Resolution
}

// SharedSettingNames returns the canonical names that a shared revision can supply.
func SharedSettingNames() []string {
	var names []string
	for _, descriptor := range catalogconfig.Descriptors() {
		if shareableDescriptor(descriptor) {
			names = append(names, descriptor.Name)
		}
	}
	return names
}

func shareableDescriptor(descriptor catalogconfig.Descriptor) bool {
	return descriptor.Scope == catalogconfig.DeploymentScope && descriptor.Name != catalogconfig.AuthorityOrigin
}

// SensitiveSharedSetting reports whether a shared setting holds a credential.
// The revision store seals these values and the checksum excludes them.
func SensitiveSharedSetting(name string) bool {
	for _, descriptor := range catalogconfig.Descriptors() {
		if descriptor.Name == name {
			return descriptor.Sensitive
		}
	}
	return false
}

// ValidateSharedValues refuses a revision that names a bootstrap, node-scope,
// or unknown setting, or a value that the catalog contract refuses. The error
// names the setting.
func ValidateSharedValues(values map[string]string) error {
	for _, name := range slices.Sorted(maps.Keys(values)) {
		if !strings.HasPrefix(name, catalogconfig.Prefix) {
			return &SharedSettingError{Name: name, Reason: "it is not a canonical catalog setting. Starport settings are bootstrap inputs"}
		}
	}
	if _, err := catalogconfig.ResolveAuthority(catalogconfig.Layer{Name: sharedRevisionLayer, Values: values}); err != nil {
		var invalid *starmaperrors.ValidationError
		if errors.As(err, &invalid) && invalid.Field != "" {
			return &SharedSettingError{Name: invalid.Field, Reason: invalid.Message}
		}
		return fmt.Errorf("shared configuration revision is invalid: %w", err)
	}
	if values[catalogconfig.Source] == CatalogSourceFile && !filepath.IsAbs(values[catalogconfig.SourceURL]) {
		return &SharedSettingError{Name: catalogconfig.SourceURL, Reason: "a shared file source requires an absolute path"}
	}
	return nil
}

// SharedValuesChecksum is the policy identity of a revision: SHA-256 over the
// canonical deployment-scope values without credentials.
func SharedValuesChecksum(values map[string]string) string {
	canonical := make(map[string]string, len(values))
	for name, value := range values {
		if !SensitiveSharedSetting(name) {
			canonical[name] = value
		}
	}
	data, err := json.Marshal(canonical, json.Deterministic(true))
	if err != nil {
		panic(fmt.Sprintf("encode shared configuration values: %v", err))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SharedSeed returns the explicit local deployment-scope values that a first
// shared revision would hold. Values can hold source credentials.
func (c *Config) SharedSeed() map[string]string {
	seed := make(map[string]string)
	if c == nil {
		return seed
	}
	for _, name := range SharedSettingNames() {
		if value, present := c.Catalog.canonicalValues[name]; present {
			seed[name] = value
		}
	}
	return seed
}

// ApplySharedRevision replaces the deployment-scope catalog values with a
// stored revision. Bootstrap and node-scope values stay local. It runs once,
// at startup, before any adapter reads catalog settings.
func (c *Config) ApplySharedRevision(revision SharedRevision) error {
	if c == nil || !c.SharedManagement() {
		return errors.New("shared configuration revision requires shared management")
	}
	if c.authority != nil {
		return errors.New("a shared configuration revision is already applied. A restart applies a newer revision")
	}
	if deploymentID := c.paths.DeploymentID; revision.DeploymentID != deploymentID {
		return &AuthorityMismatchError{Setting: "STARPORT_DEPLOYMENT_ID", Configured: deploymentID, Stored: revision.DeploymentID}
	}
	if namespace := c.ConfigNamespace(); revision.Namespace != namespace {
		return &AuthorityMismatchError{Setting: NamespaceSetting, Configured: namespace, Stored: revision.Namespace, DeploymentID: revision.DeploymentID}
	}
	if revision.Sequence <= 0 {
		return errors.New("shared configuration revision requires a positive sequence")
	}
	if err := ValidateSharedValues(revision.Values); err != nil {
		return err
	}
	if SharedValuesChecksum(revision.Values) != revision.Checksum {
		return errors.New("shared configuration revision differs from its checksum")
	}
	var local []catalogconfig.Layer
	if c.localCatalog != nil {
		local = c.localCatalog.layers
	}
	resolution, err := catalogconfig.ResolveAuthority(
		catalogconfig.Layer{Name: sharedRevisionLayer, Values: maps.Clone(revision.Values)},
		local...,
	)
	if err != nil {
		return fmt.Errorf("resolve shared configuration revision: %w", err)
	}
	layers := append([]catalogconfig.Layer{{Name: sharedRevisionLayer, Values: revision.Values}}, local...)
	values, selected := resolvedCatalogValues(resolution, layers)
	var next CatalogConfig
	if err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target: &next, Lookuper: envconfig.PrefixLookuper("STARPORT_CATALOG_", envconfig.MapLookuper(values)),
	}); err != nil {
		return fmt.Errorf("decode shared configuration revision: %w", err)
	}
	// Node values keep their loaded and anchored form.
	next.PermissionClock = c.Catalog.PermissionClock
	next.WorkspacePath, next.StateDirectory = c.Catalog.WorkspacePath, c.Catalog.StateDirectory
	next.stateDirectoryScratch = c.Catalog.stateDirectoryScratch
	if err := next.Validate(); err != nil {
		return fmt.Errorf("shared configuration revision: %w", err)
	}
	next.canonicalValues = selected
	c.Catalog = next
	c.authority = &authorityState{
		revision: AppliedRevision{
			Authority: ManagementShared, Namespace: revision.Namespace,
			Desired: revision.Sequence, Applied: revision.Sequence, Checksum: revision.Checksum,
		},
		resolution: resolution,
	}
	return nil
}

// SharedRevisionApplied reports whether catalog settings can be read. Local
// management always can. Shared management needs an applied revision.
func (c *Config) SharedRevisionApplied() bool {
	return c != nil && (!c.SharedManagement() || c.authority != nil)
}

// AppliedRevision reports the authority and revisions of this process. It
// reads process memory only.
func (c *Config) AppliedRevision() AppliedRevision {
	if c == nil {
		return AppliedRevision{Authority: ManagementLocal}
	}
	if c.authority == nil {
		report := AppliedRevision{Authority: c.ManagementMode()}
		if c.SharedManagement() {
			report.Namespace = c.ConfigNamespace()
		}
		return report
	}
	c.authority.mu.RLock()
	defer c.authority.mu.RUnlock()
	return c.authority.revision
}

// ObserveDesiredRevision records the newest stored sequence. The applied
// revision does not change until a restart applies the new revision.
func (c *Config) ObserveDesiredRevision(sequence int64) {
	if c == nil || c.authority == nil {
		return
	}
	c.authority.mu.Lock()
	defer c.authority.mu.Unlock()
	c.authority.revision.Desired = sequence
	c.authority.revision.Retained = false
}

// RetainAppliedRevision records a failed observation. The process keeps its
// applied revision as a cache of the shared authority.
func (c *Config) RetainAppliedRevision() {
	if c == nil || c.authority == nil {
		return
	}
	c.authority.mu.Lock()
	defer c.authority.mu.Unlock()
	c.authority.revision.Retained = true
}

// resolvedCatalogValues returns host environment values with descriptor
// defaults and the explicit canonical values that won the resolution.
func resolvedCatalogValues(resolution catalogconfig.Resolution, layers []catalogconfig.Layer) (map[string]string, map[string]string) {
	values := make(map[string]string)
	selected := make(map[string]string)
	for _, descriptor := range catalogconfig.Descriptors() {
		if strings.HasPrefix(descriptor.Name, starmapClockPrefix) {
			continue
		}
		value := descriptor.Default
		if origin, present := resolution.Origins[descriptor.Name]; present {
			for _, layer := range layers {
				if layer.Name == origin {
					value = layer.Values[descriptor.Name]
					selected[descriptor.Name] = value
					break
				}
			}
		}
		values[catalogEnvironmentName(descriptor.Name)] = value
	}
	return values, selected
}
