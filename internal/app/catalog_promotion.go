package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// catalogPromotionTimeout bounds one operator promotion or baseline report,
// including the runtime open, the fleet replay, and fleet acceptance.
const catalogPromotionTimeout = 5 * time.Minute

// baselinePromotionRuntime is the fleet catalog runtime that can promote its packaged baseline.
type baselinePromotionRuntime interface {
	BaselineReport() (runtimecatalog.BaselineReport, error)
	PromoteBaseline(context.Context, runtimecatalog.PromotionRequest) (runtimecatalog.PromotionReceipt, error)
}

// PromoteCatalogBaseline makes the packaged baseline of this binary the retained fleet baseline.
// It opens the fleet catalog runtime in this process and starts no gateway.
// A refusal returns a refused receipt without an error.
func PromoteCatalogBaseline(ctx context.Context, cfg *config.Config, request runtimecatalog.PromotionRequest, options ...Option) (runtimecatalog.PromotionReceipt, error) {
	if err := runtimecatalog.ValidatePromotionOperationID(request.OperationID); err != nil {
		return runtimecatalog.PromotionReceipt{}, err
	}
	if strings.TrimSpace(request.Actor) == "" {
		request.Actor = promotionActor()
	}
	var receipt runtimecatalog.PromotionReceipt
	err := withBaselinePromotionRuntime(ctx, cfg, options, func(ctx context.Context, catalog baselinePromotionRuntime) error {
		var err error
		receipt, err = catalog.PromoteBaseline(ctx, request)
		return err
	})
	return receipt, err
}

// CatalogBaselineStatus compares the packaged baseline of this binary with the retained fleet baseline.
func CatalogBaselineStatus(ctx context.Context, cfg *config.Config, options ...Option) (runtimecatalog.BaselineReport, error) {
	var report runtimecatalog.BaselineReport
	err := withBaselinePromotionRuntime(ctx, cfg, options, func(_ context.Context, catalog baselinePromotionRuntime) error {
		var err error
		report, err = catalog.BaselineReport()
		return err
	})
	return report, err
}

// withBaselinePromotionRuntime opens shared storage and the fleet catalog runtime for one operation.
// A local deployment has no fleet baseline, so it refuses before it opens storage.
func withBaselinePromotionRuntime(ctx context.Context, cfg *config.Config, options []Option, operate func(context.Context, baselinePromotionRuntime) error) (err error) {
	if cfg == nil {
		return ErrConfigRequired
	}
	if cfg.Storage.Mode != storage.StorageTypeValkey || cfg.Storage.SQL.Mode != sqlstore.TypePostgres {
		return runtimecatalog.ErrBaselinePromotionFleetOnly
	}
	build := buildOptions{factories: defaultRuntimeFactories()}
	for _, option := range options {
		option(&build)
	}
	factories := build.factories
	if factories.openStorage == nil || factories.openSQL == nil || factories.openCatalog == nil {
		return errors.New("catalog promotion factories are incomplete")
	}
	ctx, cancel := context.WithTimeout(ctx, catalogPromotionTimeout)
	defer cancel()
	store, err := factories.openStorage(cfg.RuntimeStorage())
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	db, err := factories.openSQL(cfg.Storage)
	if err != nil {
		return fmt.Errorf("open relational storage: %w", err)
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	opened, err := factories.openCatalog(ctx, store, db, catalogSettings(cfg), runtimecatalog.DeploymentLookup(cfg.LookupDeployment))
	if err != nil {
		return fmt.Errorf("open catalog: %w", err)
	}
	// Close runs under its own bound, so an expired operation still releases the lease.
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer closeCancel()
		err = errors.Join(err, opened.Close(closeCtx))
	}()
	catalog, ok := opened.(baselinePromotionRuntime)
	if !ok {
		return runtimecatalog.ErrBaselinePromotionFleetOnly
	}
	return operate(ctx, catalog)
}

// promotionActor names the operating system account that runs the command.
func promotionActor() string {
	if current, err := user.Current(); err == nil && strings.TrimSpace(current.Username) != "" {
		return current.Username
	}
	if name := strings.TrimSpace(os.Getenv("USER")); name != "" {
		return name
	}
	return "unknown"
}
