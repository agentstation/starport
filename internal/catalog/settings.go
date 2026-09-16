package catalog

import (
	"context"
	"maps"
	"strconv"
	"strings"
	"time"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/catalogs/permission/hostclock"
	"github.com/agentstation/starmap/pkg/catalogs/permission/hostclock/profile"
	protocol "github.com/agentstation/starmap/pkg/catalogs/remote"
	catalogstorage "github.com/agentstation/starmap/pkg/catalogs/storage"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/remote"
	"github.com/agentstation/starmap/runtime"
)

// cascadeFallbackAfterFailures is how many consecutive stream failures the
// cascade accepts before it polls the upstream manifest. A short streaming
// outage recovers on its own, so polling starts only after the stream proved
// it cannot hold.
const cascadeFallbackAfterFailures = 3

// Settings contains the catalog values one connected runtime reads.
// It mirrors the canonical Starmap settings with plain Go types.
// The configuration package names no Starmap options. This package owns the translation.
type Settings struct {
	// BaselineDirectory selects the host-owned immutable embedded export.
	BaselineDirectory string
	// SourceCacheDirectory selects the host-owned parent for source caches and checkouts.
	SourceCacheDirectory string
	// InstanceID and DeploymentID bind the runtime directory to this Starport process.
	InstanceID   string
	DeploymentID string

	// Values retains the canonical settings that have no gateway-specific projection.
	Values map[string]string

	// PermissionClock contains the canonical host bounds. The runtime owns its monitor.
	PermissionClock profile.Config

	// Source selects the catalog source kind.
	Source string

	// SourceURL is the safe source endpoint or the file identity.
	SourceURL string

	// SourceAPIKey authenticates this instance to an upstream deployment.
	SourceAPIKey string

	// SourceRepository names the signed publication repository.
	SourceRepository string

	// SourceChannel names the publication channel.
	SourceChannel string

	// SourceSignerWorkflow names the workflow that signed a publication.
	SourceSignerWorkflow string

	// SourceToken reads a GitHub release.
	SourceToken string

	// SourcePollInterval sets the polling interval for the source.
	SourcePollInterval time.Duration

	// SourceStartupPolicy decides what startup does without a source answer.
	SourceStartupPolicy string

	// SourceAuthorityID pins the authority used by require_authority.
	SourceAuthorityID string

	// SourcePolicyID pins the permission policy within the selected authority.
	SourcePolicyID string

	// SourceMaxAge is the oldest publication this instance accepts.
	SourceMaxAge time.Duration

	// SourceMaxHops bounds the publication chain.
	SourceMaxHops int

	// AcquisitionEnabled controls automatic observations. Explicit refresh still checks network and authority policy.
	AcquisitionEnabled bool

	// AcquisitionInterval is the period between provider observations.
	AcquisitionInterval time.Duration

	// WorkspacePath is the local catalog workspace directory. It holds the
	// catalog files an operator supplies and nothing this process owns.
	WorkspacePath string

	// StateDirectory is where this process keeps the state the connected
	// runtime retains: the layer store, the instance identity seed, and the
	// source discovery record. It belongs to one process on one machine.
	StateDirectory string

	// ListenAddress is the host and port this gateway serves.
	// Changing this address does not change the durable runtime identity.
	ListenAddress string

	// StartupSpread spreads the first source read across a fleet.
	StartupSpread time.Duration

	// TransferIdleTimeout ends a transfer that stops making progress.
	TransferIdleTimeout time.Duration

	// TransferMaxDuration bounds one complete transfer.
	TransferMaxDuration time.Duration

	// RefreshTimeout is an added cap on one refresh run.
	RefreshTimeout time.Duration
}

// starmapOptions translates the settings into Starmap runtime options. It is
// the only place that names a Starmap option, so the settings contract and the
// Starmap contract stay one translation apart.
//
// Starmap receives the source kind that the operator selected.
// A private source never falls back to the public channel.
// If runtime.Open fails, that error propagates without a source change.
func (s Settings) starmapOptions() ([]runtime.Option, error) {
	monitor, err := hostclock.NewMonitor(s.PermissionClock)
	if err != nil {
		return nil, err
	}
	parsed, err := catalogconfig.Parse(s.catalogValues())
	if err != nil {
		return nil, err
	}
	owner := s.directoryOwner()
	if err := owner.Validate(); err != nil {
		return nil, err
	}
	options := append(parsed.Options(), runtime.WithDirectoryOwner(owner))
	if monitor != nil {
		options = append(options, runtime.WithPermissionClockMonitor(monitor))
	}

	// Record the listen address without changing the durable runtime identity.
	if address := strings.TrimSpace(s.ListenAddress); address != "" {
		options = append(options, runtime.WithListenAddress(address))
	}
	return options, nil
}

// catalogValues projects typed host values into the shared parser.
// Explicit zero and false values always replace the corresponding defaults.
func (s Settings) catalogValues() map[string]string {
	values := maps.Clone(s.Values)
	if values == nil {
		values = make(map[string]string)
	}
	for name, value := range map[string]string{
		catalogconfig.Source:              s.Source,
		catalogconfig.SourceAPIKey:        s.SourceAPIKey,
		catalogconfig.SourceToken:         s.SourceToken,
		catalogconfig.SourcePollInterval:  s.SourcePollInterval.String(),
		catalogconfig.SourceStartupPolicy: s.SourceStartupPolicy,
		catalogconfig.SourceMaxAge:        s.SourceMaxAge.String(),
		catalogconfig.SourceMaxHops:       strconv.Itoa(s.SourceMaxHops),
		catalogconfig.AcquisitionEnabled:  strconv.FormatBool(s.AcquisitionEnabled),
		catalogconfig.AcquisitionInterval: s.AcquisitionInterval.String(),
		catalogconfig.StartupSpread:       s.StartupSpread.String(),
		catalogconfig.TransferIdleTimeout: s.TransferIdleTimeout.String(),
		catalogconfig.TransferMaxDuration: s.TransferMaxDuration.String(),
		catalogconfig.RefreshTimeout:      s.RefreshTimeout.String(),
	} {
		values[name] = value
	}
	for name, value := range map[string]string{
		catalogconfig.SourceURL:            s.SourceURL,
		catalogconfig.SourceRepository:     s.SourceRepository,
		catalogconfig.SourceChannel:        s.SourceChannel,
		catalogconfig.SourceSignerWorkflow: s.SourceSignerWorkflow,
		catalogconfig.SourceAuthorityID:    s.SourceAuthorityID,
		catalogconfig.SourcePolicyID:       s.SourcePolicyID,
		catalogconfig.WorkspacePath:        s.WorkspacePath,
		catalogconfig.StateDirectory:       s.StateDirectory,
	} {
		if value == "" {
			delete(values, name)
		} else {
			values[name] = value
		}
	}
	return values
}

// cascadeSource builds the Starmap cascade source of a deployment that reads
// another Starmap runtime.
//
// The Starmap root package refuses to build this source itself, because the
// cascade subscriber imports that root package. This is therefore the one
// place that maps the canonical settings onto the subscriber transport.
func (s Settings) cascadeSource(ctx context.Context) (*remote.Source, error) {
	url := strings.TrimSpace(s.SourceURL)
	if url == "" {
		return nil, &starmaperrors.ConfigError{
			Component: "catalog source",
			Message:   "the starmap catalog source needs STARPORT_CATALOG_SOURCE_URL",
		}
	}
	transfer := protocol.DefaultTransferPolicy()
	if s.TransferIdleTimeout > 0 {
		transfer.IdleTimeout = s.TransferIdleTimeout
	}
	if s.TransferMaxDuration > 0 {
		transfer.MaxDuration = s.TransferMaxDuration
	}
	return remote.NewSource(ctx, remote.SourceConfig{
		Subscriber: remote.Config{
			BaseURL:        url,
			CatalogStore:   catalogstorage.NewMemory(),
			APIKey:         strings.TrimSpace(s.SourceAPIKey),
			StartupSpread:  s.StartupSpread,
			TransferPolicy: &transfer,
			PollingFallback: &remote.PollingFallbackPolicy{
				AfterFailures: cascadeFallbackAfterFailures,
				Interval:      remote.DefaultFallbackPollInterval,
			},
		},
		MaxHops: s.SourceMaxHops,
		MaxAge:  s.SourceMaxAge,
	})
}

func (s Settings) directoryOwner() runtime.DirectoryOwner {
	instance, deployment := s.InstanceID, s.DeploymentID
	if instance == "" {
		instance = "default"
	}
	if deployment == "" {
		deployment = "local"
	}
	return runtime.DirectoryOwner{Product: "starport", Deployment: deployment, Instance: instance}
}
