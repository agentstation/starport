package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sethvargo/go-envconfig"
)

// Catalog source kinds. They are the closed set the Starmap settings contract
// names. Each kind selects where one connected runtime reads its catalog.
const (
	// CatalogSourcePublic reads the public Starmap publication channel.
	CatalogSourcePublic = "public"
	// CatalogSourceGitHub reads a signed GitHub release channel.
	CatalogSourceGitHub = "github"
	// CatalogSourceStarmap reads an upstream Starmap deployment.
	CatalogSourceStarmap = "starmap"
	// CatalogSourceFile reads a catalog file on this machine.
	CatalogSourceFile = "file"
	// CatalogSourceEmbedded reads the catalog the binary carries.
	CatalogSourceEmbedded = "embedded"
)

// Catalog startup policies. They decide what startup does when the source
// answers with nothing.
const (
	// CatalogStartupPreferSource starts on the embedded catalog when the
	// source does not answer, and adopts the source when it answers.
	CatalogStartupPreferSource = "prefer_source"
	// CatalogStartupRequireSource refuses to start until the source answers.
	CatalogStartupRequireSource = "require_source"
)

// DefaultCatalogSourceRepository is the signed publication repository.
const DefaultCatalogSourceRepository = "agentstation/starmap"

// DefaultCatalogSourceChannel is the publication channel of the repository.
const DefaultCatalogSourceChannel = "catalog-latest"

// catalogStateSubdirectory is where a Starport process keeps its catalog state
// under the user state root.
const catalogStateSubdirectory = "starport/catalog"

// stateHomeEnvironment names the XDG user state root.
const stateHomeEnvironment = "XDG_STATE_HOME"

// CatalogConfig holds the canonical Starmap catalog settings. Starport uses
// the suffixes Starmap names, with the gateway prefix. One connected runtime
// reads one source and, when acquisition is enabled, observes providers on its
// own schedule.
//
// Catalog-acquisition credentials stay separate from inference credentials:
// SourceAPIKey speaks the Starmap protocol, and SourceToken reads a GitHub
// release. Neither one pays a provider.
type CatalogConfig struct {
	// Source selects the catalog source kind.
	Source string `env:"SOURCE,default=public"`

	// SourceURL is the safe source endpoint, or the file identity when the
	// source kind is a file.
	SourceURL string `env:"SOURCE_URL" redact:"url"`

	// SourceAPIKey authenticates this instance to an upstream Starmap
	// deployment. It is not a provider credential.
	SourceAPIKey string `env:"SOURCE_API_KEY" secret:"true"`

	// SourceRepository names the signed publication repository.
	SourceRepository string `env:"SOURCE_REPOSITORY,default=agentstation/starmap"`

	// SourceChannel names the publication channel of that repository.
	SourceChannel string `env:"SOURCE_CHANNEL,default=catalog-latest"`

	// SourceSignerWorkflow names the workflow that must have signed a
	// publication. Empty selects the publisher preset.
	SourceSignerWorkflow string `env:"SOURCE_SIGNER_WORKFLOW"`

	// SourceToken is an optional GitHub API token. It raises the anonymous
	// rate limit and reads a private repository.
	SourceToken string `env:"SOURCE_TOKEN" secret:"true"`

	// SourcePollInterval bounds how often this instance asks the source for
	// a newer publication.
	SourcePollInterval time.Duration `env:"SOURCE_POLL_INTERVAL,default=1h"`

	// SourceStartupPolicy decides what startup does without a source answer.
	SourceStartupPolicy string `env:"SOURCE_STARTUP_POLICY,default=prefer_source"`

	// SourceMaxAge is the oldest publication this instance accepts.
	SourceMaxAge time.Duration `env:"SOURCE_MAX_AGE,default=6h"`

	// SourceMaxHops bounds the publication chain this instance follows.
	SourceMaxHops int `env:"SOURCE_MAX_HOPS,default=8"`

	// AcquisitionEnabled decides whether this instance observes providers.
	AcquisitionEnabled bool `env:"ACQUISITION_ENABLED,default=true"`

	// AcquisitionInterval is the period between provider observations. Zero
	// means one observation at startup and no repeat.
	AcquisitionInterval time.Duration `env:"ACQUISITION_INTERVAL,default=4h"`

	// WorkspacePath is an optional local catalog workspace directory. It
	// holds catalog files an operator supplies. It is never the state
	// directory, because a workspace can sit on a volume many instances share.
	WorkspacePath string `env:"WORKSPACE_PATH"`

	// StateDirectory is where this process keeps the state the connected
	// runtime retains: the layer store, the instance identity seed, and the
	// source discovery record. It must belong to one process on one machine,
	// because two processes that share a seed derive one instance identity and
	// the runtime lease then fences nothing. An empty value resolves to the
	// process-local user state directory.
	StateDirectory string `env:"STATE_DIR"`

	// StartupSpread spreads the first source read of a fleet, so many
	// instances that start together do not ask at the same moment.
	StartupSpread time.Duration `env:"STARTUP_SPREAD,default=15m"`

	// TransferIdleTimeout ends a transfer that stops making progress.
	TransferIdleTimeout time.Duration `env:"TRANSFER_IDLE_TIMEOUT,default=2m"`

	// TransferMaxDuration bounds one complete transfer. Zero is invalid,
	// because a transfer without a bound never ends.
	TransferMaxDuration time.Duration `env:"TRANSFER_MAX_DURATION,default=60m"`

	// RefreshTimeout is an added cap on one refresh run. Zero adds no cap,
	// and the transfer bounds alone end a run that does not progress.
	RefreshTimeout time.Duration `env:"REFRESH_TIMEOUT,default=0s"`
}

// DefaultCatalogConfig returns the canonical catalog settings. It states the
// same values the environment tags name, so a caller that builds a
// configuration without the environment starts from the shipped contract.
func DefaultCatalogConfig() CatalogConfig {
	return CatalogConfig{
		Source:              CatalogSourcePublic,
		SourceRepository:    DefaultCatalogSourceRepository,
		SourceChannel:       DefaultCatalogSourceChannel,
		SourcePollInterval:  time.Hour,
		SourceStartupPolicy: CatalogStartupPreferSource,
		SourceMaxAge:        6 * time.Hour,
		SourceMaxHops:       8,
		AcquisitionEnabled:  true,
		AcquisitionInterval: 4 * time.Hour,
		StartupSpread:       15 * time.Minute,
		TransferIdleTimeout: 2 * time.Minute,
		TransferMaxDuration: 60 * time.Minute,
	}
}

// Validate refuses a catalog setting the runtime cannot honor.
func (c *CatalogConfig) Validate() error {
	switch c.Source {
	case CatalogSourcePublic, CatalogSourceGitHub, CatalogSourceStarmap,
		CatalogSourceFile, CatalogSourceEmbedded:
	default:
		return fmt.Errorf(
			"catalog source %q is not one of public, github, starmap, file, embedded",
			c.Source,
		)
	}
	switch c.SourceStartupPolicy {
	case CatalogStartupPreferSource, CatalogStartupRequireSource:
	default:
		return fmt.Errorf(
			"catalog source startup policy %q is not one of prefer_source, require_source",
			c.SourceStartupPolicy,
		)
	}
	if c.Source == CatalogSourceStarmap && strings.TrimSpace(c.SourceURL) == "" {
		return fmt.Errorf("catalog source starmap requires a source URL")
	}
	if c.Source == CatalogSourceFile && strings.TrimSpace(c.SourceURL) == "" {
		return fmt.Errorf("catalog source file requires a source URL")
	}
	if c.SourcePollInterval < 0 {
		return fmt.Errorf("catalog source poll interval cannot be negative")
	}
	if c.SourceMaxAge < 0 {
		return fmt.Errorf("catalog source max age cannot be negative")
	}
	if c.SourceMaxHops <= 0 {
		return fmt.Errorf("catalog source max hops must be positive")
	}
	if c.AcquisitionInterval < 0 {
		return fmt.Errorf("catalog acquisition interval cannot be negative")
	}
	if c.StartupSpread < 0 {
		return fmt.Errorf("catalog startup spread cannot be negative")
	}
	if c.TransferIdleTimeout <= 0 {
		return fmt.Errorf("catalog transfer idle timeout must be positive")
	}
	if c.TransferMaxDuration <= 0 {
		return fmt.Errorf("catalog transfer max duration must be positive")
	}
	if c.RefreshTimeout < 0 {
		return fmt.Errorf("catalog refresh timeout cannot be negative")
	}
	workspace := strings.TrimSpace(c.WorkspacePath)
	if workspace != "" && workspace == strings.TrimSpace(c.StateDirectory) {
		return fmt.Errorf(
			"catalog state directory cannot be the workspace path, " +
				"because a shared workspace would give two instances one identity",
		)
	}
	return nil
}

// ResolveStateDirectory returns the catalog state directory of this process.
// An operator value wins. An empty value resolves to the user state root,
// which is process-local: it is never the workspace path and never a storage
// path, so two instances that share a volume still hold two identities.
func ResolveStateDirectory(configured string) (string, error) {
	if directory := strings.TrimSpace(configured); directory != "" {
		return directory, nil
	}
	if root := strings.TrimSpace(os.Getenv(stateHomeEnvironment)); root != "" {
		return filepath.Join(root, filepath.FromSlash(catalogStateSubdirectory)), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	if home == "" {
		return "", fmt.Errorf("user home directory is empty")
	}
	return filepath.Join(
		home, ".local", "state", filepath.FromSlash(catalogStateSubdirectory),
	), nil
}

// RemovedSettingError reports one environment variable this gateway no longer
// reads. It names the setting that replaced the removed one, so an operator
// repairs the deployment without a search.
type RemovedSettingError struct {
	// Name is the complete environment variable the deployment still sets.
	Name string
	// Replacement is the complete environment variable to set instead.
	Replacement string
	// Reason states what changed.
	Reason string
}

// Error states the removed variable and its replacement.
func (e *RemovedSettingError) Error() string {
	return fmt.Sprintf(
		"%s is removed: %s. Set %s instead.",
		e.Name, e.Reason, e.Replacement,
	)
}

// removedCatalogSettings maps every removed catalog variable to the canonical
// setting that replaced it. The local-or-remote catalog choice is gone: one
// connected runtime reads one source, so the variables that selected a remote
// publication and a separate local refresh schedule no longer have a meaning.
var removedCatalogSettings = []RemovedSettingError{
	{
		Name:        "STARPORT_CATALOG_REFRESH_ON_START",
		Replacement: "STARPORT_CATALOG_ACQUISITION_ENABLED",
		Reason:      "the connected runtime always reads its source at startup",
	},
	{
		Name:        "STARPORT_CATALOG_REFRESH_INTERVAL",
		Replacement: "STARPORT_CATALOG_ACQUISITION_INTERVAL",
		Reason:      "provider observation owns its own schedule",
	},
	{
		Name:        "STARPORT_CATALOG_REMOTE_URL",
		Replacement: "STARPORT_CATALOG_SOURCE_URL",
		Reason:      "one source setting replaces the local and remote choice",
	},
	{
		Name:        "STARPORT_CATALOG_REMOTE_API_KEY",
		Replacement: "STARPORT_CATALOG_SOURCE_API_KEY",
		Reason:      "one source setting replaces the local and remote choice",
	},
	{
		Name:        "STARPORT_CATALOG_REMOTE_ACTIVATION_INTERVAL",
		Replacement: "STARPORT_CATALOG_SOURCE_POLL_INTERVAL",
		Reason:      "the source reports a new publication instead of a poll loop",
	},
}

// RemovedCatalogSettings returns every removed catalog variable with its
// replacement. Documentation and startup read the same list.
func RemovedCatalogSettings() []RemovedSettingError {
	settings := make([]RemovedSettingError, len(removedCatalogSettings))
	copy(settings, removedCatalogSettings)
	return settings
}

// checkRemovedSettings refuses startup when a deployment still sets a removed
// catalog variable. A silent ignore would leave the operator believing a
// setting still applies, so startup fails and names the replacement.
func checkRemovedSettings(lookuper envconfig.Lookuper) error {
	if lookuper == nil {
		return nil
	}
	for _, removed := range removedCatalogSettings {
		if _, ok := lookuper.Lookup(removed.Name); ok {
			setting := removed
			return &setting
		}
	}
	return nil
}
