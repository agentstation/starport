package catalog

import (
	"context"
	"strings"
	"time"

	"github.com/agentstation/starmap"
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

	// AcquisitionEnabled decides whether this instance observes providers.
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
	options := []runtime.Option{
		runtime.WithCatalogSource(s.Source),
		runtime.WithSourceStartupPolicy(s.SourceStartupPolicy),
		runtime.WithSourceAuthority(s.SourceAuthorityID, s.SourcePolicyID),
		runtime.WithSourcePollInterval(s.SourcePollInterval),
		runtime.WithSourceMaxAge(s.SourceMaxAge),
		runtime.WithSourceMaxHops(s.SourceMaxHops),
		runtime.WithAcquisitionEnabled(s.AcquisitionEnabled),
		runtime.WithStartupSpread(s.StartupSpread),
		runtime.WithTransferIdleTimeout(s.TransferIdleTimeout),
		runtime.WithTransferMaxDuration(s.TransferMaxDuration),
	}
	if monitor != nil {
		options = append(options, runtime.WithPermissionClockMonitor(monitor))
	}
	if url := strings.TrimSpace(s.SourceURL); url != "" {
		options = append(options, runtime.WithSourceURL(url))
	}
	if key := strings.TrimSpace(s.SourceAPIKey); key != "" {
		options = append(options, runtime.WithSourceAPIKey(key))
	}
	if repository := strings.TrimSpace(s.SourceRepository); repository != "" {
		options = append(options, runtime.WithSourceRepository(repository))
	}
	if channel := strings.TrimSpace(s.SourceChannel); channel != "" {
		options = append(options, runtime.WithSourceChannel(channel))
	}
	if workflow := strings.TrimSpace(s.SourceSignerWorkflow); workflow != "" {
		options = append(options, runtime.WithSourceSignerWorkflow(workflow))
	}
	if token := strings.TrimSpace(s.SourceToken); token != "" {
		options = append(options, runtime.WithSourceToken(token))
	}
	if s.AcquisitionInterval > 0 {
		options = append(options, runtime.WithAcquisitionInterval(s.AcquisitionInterval))
	}
	if s.RefreshTimeout > 0 {
		options = append(options, runtime.WithRefreshTimeout(s.RefreshTimeout))
	}
	if path := strings.TrimSpace(s.WorkspacePath); path != "" {
		options = append(options, runtime.WithClientOptions(starmap.WithCatalogPath(path)))
	}

	// The state directory is never the workspace path. A workspace can sit on
	// a volume a fleet shares, and a shared identity seed would give two
	// instances one lease holder, which fences nothing.
	if directory := strings.TrimSpace(s.StateDirectory); directory != "" {
		options = append(options, runtime.WithStateDirectory(directory))
	}

	// Record the listen address without changing the durable runtime identity.
	if address := strings.TrimSpace(s.ListenAddress); address != "" {
		options = append(options, runtime.WithListenAddress(address))
	}
	return options, nil
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
