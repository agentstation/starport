// Package diagnosis owns read-only startup checks for Starport.
package diagnosis

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/catalogstore"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/providerauth"
	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/storage"
)

const (
	// StatusPass means that a check satisfied its contract.
	StatusPass = "pass"
	// StatusFail means that a check did not satisfy its contract.
	StatusFail = "fail"
	// StatusSkip means that a check needs an explicit probe.
	StatusSkip = "skip"
)

// Options selects passive checks or explicit storage probes.
type Options struct {
	Probe bool
}

// Check is one stable diagnostic result.
type Check struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Report contains all diagnostic results in execution order.
type Report struct {
	OK      bool    `json:"ok"`
	Probed  bool    `json:"probed"`
	Checks  []Check `json:"checks"`
	Passed  int     `json:"passed"`
	Failed  int     `json:"failed"`
	Skipped int     `json:"skipped"`
}

type dependencies struct {
	loadConfig     func(context.Context) (*config.Config, error)
	resolvePaths   func() (config.Paths, error)
	openStorage    func(storage.Config) (storage.KVStore, error)
	transports     func() (*connectors.TransportRegistry, error)
	authentication func() (*providerauth.Registry, error)
}

// Run performs production diagnosis without constructing the server.
func Run(ctx context.Context, options Options) Report {
	service := service{dependencies: dependencies{
		loadConfig: config.LoadWithDefaults, resolvePaths: config.PlatformPaths,
		openStorage:    storage.OpenReadOnly,
		transports:     connectors.ProductionTransportRegistry,
		authentication: providerauth.ProductionRegistry,
	}}
	return service.run(ctx, options)
}

type service struct {
	dependencies dependencies
}

func (s service) run(ctx context.Context, options Options) Report {
	report := Report{OK: true, Probed: options.Probe, Checks: make([]Check, 0, 7)}
	paths, err := s.dependencies.resolvePaths()
	if err != nil {
		report.addFailure("paths", "platform paths could not be resolved")
	} else {
		report.addPass("paths", fmt.Sprintf("configuration file: %s", paths.ConfigFile))
	}

	cfg, err := s.dependencies.loadConfig(ctx)
	if err != nil {
		report.addFailure("configuration", config.OperatorError(err).Error())
		report.addSkip("master_key", "configuration is unavailable")
		report.addSkip("storage", "configuration is unavailable")
		report.addSkip("identities", "storage is unavailable")
		report.addSkip("catalog", "configuration is unavailable")
		report.addSkip("adapters", "catalog is unavailable")
		return report
	}
	report.addPass("configuration", "effective configuration is valid")
	if strings.TrimSpace(cfg.Security.MasterKey) == "" {
		report.addFailure("master_key", "provider credential master key is not configured")
	} else {
		report.addPass("master_key", "provider credential master key is configured")
	}

	var store storage.KVStore
	if !options.Probe {
		report.addSkip("storage", "use --probe to open configured storage in read-only mode")
		report.addSkip("identities", "use --probe to inspect configured identity storage")
	} else {
		store, err = s.dependencies.openStorage(cfg.Storage.RuntimeStorage())
		switch {
		case errors.Is(err, storage.ErrReadOnlyRecoveryRequired):
			report.addSkip(
				"storage",
				"Badger needs recovery; start and stop \"starport serve\" cleanly, then rerun the probe",
			)
			report.addSkip("identities", "storage inspection is unavailable until Badger recovery")
		case errors.Is(err, storage.ErrReadOnlyUnsupported):
			report.addSkip(
				"storage",
				"read-only Badger inspection is unavailable on this platform; verify startup with \"starport serve\"",
			)
			report.addSkip("identities", "read-only storage inspection is unavailable on this platform")
		case err != nil:
			report.addFailure("storage", "configured storage could not be opened in read-only mode")
			report.addSkip("identities", "storage is unavailable")
		default:
			report.addPass("storage", "configured storage opened in read-only mode")
			repository, openErr := identity.Open(store)
			if openErr != nil {
				report.addFailure("identities", "gateway identity repository could not be opened")
			} else {
				records, listErr := repository.List(ctx, 1)
				switch {
				case listErr != nil:
					report.addFailure("identities", "gateway identity storage could not be read")
				case len(records) == 0:
					report.addFailure("identities", "gateway identity storage is empty; run \"starport init\"")
				default:
					report.addPass("identities", "gateway identity storage contains an identity")
				}
			}
		}
	}

	state, stateErr := loadCatalogState(ctx, cfg.Catalog.WorkspacePath, store)
	if stateErr != nil {
		report.addFailure("catalog", "Starmap catalog state could not be loaded")
		report.addSkip("adapters", "catalog is unavailable")
	} else {
		plane, openErr := runtimecatalog.Open(staticSource{state: state})
		if openErr != nil {
			report.addFailure("catalog", "Starmap catalog projection could not be opened")
			report.addSkip("adapters", "catalog projection is unavailable")
		} else {
			report.addPass(
				"catalog",
				fmt.Sprintf(
					"Starmap generation %s contains %d providers",
					state.GenerationID,
					len(state.Catalog.Providers().List()),
				),
			)
			s.checkAdapters(cfg, plane, state.Catalog, &report)
		}
	}

	if store != nil {
		if closeErr := store.Close(); closeErr != nil {
			report.addFailure("storage_close", "configured storage could not be closed")
		}
	}
	return report
}

func (s service) checkAdapters(
	cfg *config.Config,
	plane *runtimecatalog.ControlPlane,
	source *catalogs.Catalog,
	report *Report,
) {
	if err := cfg.ResolveProviders(context.Background(), source.Providers()); err != nil {
		report.addFailure("adapters", "provider configuration could not be resolved")
		return
	}
	transportRegistry, err := s.dependencies.transports()
	if err != nil {
		report.addFailure("adapters", "provider transport registry could not be created")
		return
	}
	authenticationRegistry, err := s.dependencies.authentication()
	if err != nil {
		report.addFailure("adapters", "provider authentication registry could not be created")
		return
	}
	providerConfigs := providers.Configurations(cfg.Providers)
	activations, err := providers.Activate(
		source,
		transportRegistry,
		authenticationRegistry,
		providerConfigs,
	)
	if err != nil {
		report.addFailure("adapters", "provider adapters could not be activated")
		return
	}
	if len(activations) == 0 {
		report.addFailure("adapters", "no inference provider is configured")
		return
	}
	if err := plane.ReplaceAdapters(providers.Availability(activations)); err != nil {
		report.addFailure("adapters", "provider availability could not be projected")
		return
	}
	routes := plane.Current().Routes()
	if len(routes) == 0 {
		report.addFailure("adapters", "configured adapters expose no routable Starmap offering")
		return
	}
	report.addPass(
		"adapters",
		fmt.Sprintf("%d configured provider adapters expose %d routable offerings", len(activations), len(routes)),
	)
}

func loadCatalogState(
	ctx context.Context,
	workspacePath string,
	store storage.KVStore,
) (starmap.CatalogState, error) {
	if store != nil {
		generationStore, err := runtimecatalog.NewGenerationStore(store)
		if err != nil {
			return starmap.CatalogState{}, err
		}
		generation, currentErr := generationStore.Current(ctx)
		switch {
		case currentErr == nil:
			catalog, decodeErr := catalogstore.DecodeCatalogPayload(generation.Payload)
			if decodeErr != nil {
				return starmap.CatalogState{}, fmt.Errorf("decode current catalog generation: %w", decodeErr)
			}
			return starmap.CatalogState{
				Catalog: catalog, GenerationID: generation.Manifest.GenerationID,
				GeneratedAt: generation.Manifest.GeneratedAt, Sequence: 1,
			}, nil
		case !errors.Is(currentErr, starmaperrors.ErrNotFound):
			return starmap.CatalogState{}, currentErr
		}
	}

	options := make([]starmap.Option, 0, 1)
	if strings.TrimSpace(workspacePath) != "" {
		options = append(options, starmap.WithCatalogPath(workspacePath))
	}
	client, err := starmap.NewContext(ctx, options...)
	if err != nil {
		return starmap.CatalogState{}, err
	}
	return client.CurrentCatalogState(), nil
}

type staticSource struct {
	state starmap.CatalogState
}

func (s staticSource) CurrentCatalogState() starmap.CatalogState { return s.state }

func (r *Report) addPass(id, message string) {
	r.Checks = append(r.Checks, Check{ID: id, Status: StatusPass, Message: message})
	r.Passed++
}

func (r *Report) addFailure(id, message string) {
	r.Checks = append(r.Checks, Check{ID: id, Status: StatusFail, Message: message})
	r.Failed++
	r.OK = false
}

func (r *Report) addSkip(id, message string) {
	r.Checks = append(r.Checks, Check{ID: id, Status: StatusSkip, Message: message})
	r.Skipped++
}
