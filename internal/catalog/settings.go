package catalog

import (
	"context"
	"strings"
	"time"

	"github.com/agentstation/starmap"
	protocol "github.com/agentstation/starmap/pkg/catalogs/remote"
	catalogstorage "github.com/agentstation/starmap/pkg/catalogs/storage"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/remote"
)

// cascadeFallbackAfterFailures is how many consecutive stream failures the
// cascade accepts before it polls the upstream manifest. A short streaming
// outage recovers on its own, so polling starts only after the stream proved
// it cannot hold.
const cascadeFallbackAfterFailures = 3

// Settings are the catalog settings one connected runtime reads. They mirror
// the canonical Starmap settings contract with plain Go types, so the
// configuration package names no Starmap option and this package alone owns
// the translation.
type Settings struct {
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

	// SourcePollInterval bounds how often the source is asked.
	SourcePollInterval time.Duration

	// SourceStartupPolicy decides what startup does without a source answer.
	SourceStartupPolicy string

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

	// ListenAddress is the host and port this gateway serves. It joins the
	// identity seed and the host name in the instance identity, so two
	// processes on one host hold two identities and the runtime lease fences
	// one holder.
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
// A private source never falls back to the public channel: the source kind
// reaches Starmap exactly as the operator selected it, and Starmap fails Open
// instead of reading a different source.
func (s Settings) starmapOptions() []starmap.Option {
	options := []starmap.Option{
		starmap.WithCatalogSource(s.Source),
		starmap.WithSourceStartupPolicy(s.SourceStartupPolicy),
		starmap.WithSourcePollInterval(s.SourcePollInterval),
		starmap.WithSourceMaxAge(s.SourceMaxAge),
		starmap.WithSourceMaxHops(s.SourceMaxHops),
		starmap.WithAcquisitionEnabled(s.AcquisitionEnabled),
		starmap.WithStartupSpread(s.StartupSpread),
		starmap.WithTransferIdleTimeout(s.TransferIdleTimeout),
		starmap.WithTransferMaxDuration(s.TransferMaxDuration),
	}
	if url := strings.TrimSpace(s.SourceURL); url != "" {
		options = append(options, starmap.WithSourceURL(url))
	}
	if key := strings.TrimSpace(s.SourceAPIKey); key != "" {
		options = append(options, starmap.WithSourceAPIKey(key))
	}
	if repository := strings.TrimSpace(s.SourceRepository); repository != "" {
		options = append(options, starmap.WithSourceRepository(repository))
	}
	if channel := strings.TrimSpace(s.SourceChannel); channel != "" {
		options = append(options, starmap.WithSourceChannel(channel))
	}
	if workflow := strings.TrimSpace(s.SourceSignerWorkflow); workflow != "" {
		options = append(options, starmap.WithSourceSignerWorkflow(workflow))
	}
	if token := strings.TrimSpace(s.SourceToken); token != "" {
		options = append(options, starmap.WithSourceToken(token))
	}
	if s.AcquisitionInterval > 0 {
		options = append(options, starmap.WithAcquisitionInterval(s.AcquisitionInterval))
	}
	if s.RefreshTimeout > 0 {
		options = append(options, starmap.WithRefreshTimeout(s.RefreshTimeout))
	}
	if path := strings.TrimSpace(s.WorkspacePath); path != "" {
		options = append(options, starmap.WithCatalogPath(path))
	}

	// The state directory is never the workspace path. A workspace can sit on
	// a volume a fleet shares, and a shared identity seed would give two
	// instances one lease holder, which fences nothing.
	if directory := strings.TrimSpace(s.StateDirectory); directory != "" {
		options = append(options, starmap.WithStateDirectory(directory))
	}

	// The listen address separates two processes that share one host and one
	// state root, so each one derives its own instance identity.
	if address := strings.TrimSpace(s.ListenAddress); address != "" {
		options = append(options, starmap.WithListenAddress(address))
	}
	return options
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
