package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/agentstation/starmap"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

const (
	// catalogBaselineStatusTimeout bounds one baseline report, including the fleet replay.
	catalogBaselineStatusTimeout = 5 * time.Minute
	// promotionPollInterval is the period between two reads of the promotion request.
	promotionPollInterval = 2 * time.Second
)

// ErrBaselinePromotionPending reports a promotion request that no leader settled within the wait.
var ErrBaselinePromotionPending = errors.New("baseline promotion is still pending")

// promotionRequests is the shared promotion request record of one fleet deployment.
type promotionRequests interface {
	Submit(context.Context, runtimecatalog.PromotionRequest) (runtimecatalog.PromotionRecord, error)
	Read(context.Context, string) (runtimecatalog.PromotionRecord, bool, error)
	Leader(context.Context) (string, error)
}

// baselineObserver is the lease-free fleet catalog runtime of one baseline report.
type baselineObserver interface {
	BaselineReport() (runtimecatalog.BaselineReport, error)
	Close(context.Context) error
}

// PromotionRecorded receives the pending request and the lease holder that executes it.
// The holder is empty when no gateway leads the fleet.
type PromotionRecorded func(record runtimecatalog.PromotionRecord, leader string)

// PromoteCatalogBaseline records a request to make the packaged baseline of this binary
// the retained fleet baseline. The gateway that holds the publication lease executes it.
// The command never takes the lease and never opens a gateway state directory.
// It waits for the outcome until wait ends. A refusal returns a refused receipt without an error.
func PromoteCatalogBaseline(
	ctx context.Context,
	cfg *config.Config,
	request runtimecatalog.PromotionRequest,
	wait time.Duration,
	recorded PromotionRecorded,
	options ...Option,
) (receipt runtimecatalog.PromotionReceipt, err error) {
	if err := runtimecatalog.ValidatePromotionOperationID(request.OperationID); err != nil {
		return runtimecatalog.PromotionReceipt{}, err
	}
	if wait <= 0 {
		return runtimecatalog.PromotionReceipt{}, errors.New("the promotion wait must be positive")
	}
	factories, err := baselineCommandFactories(cfg, options)
	if err != nil {
		return runtimecatalog.PromotionReceipt{}, err
	}
	if strings.TrimSpace(request.Actor) == "" {
		request.Actor = promotionActor()
	}
	if request.PackagedGenerationID == "" {
		generation, err := starmap.EmbeddedGeneration()
		if err != nil {
			return runtimecatalog.PromotionReceipt{}, fmt.Errorf("read the packaged baseline: %w", err)
		}
		request.PackagedGenerationID = generation.Manifest.GenerationID
	}
	store, err := factories.openStorage(cfg.RuntimeStorage())
	if err != nil {
		return runtimecatalog.PromotionReceipt{}, fmt.Errorf("open storage: %w", err)
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	db, err := factories.openSQL(cfg.Storage)
	if err != nil {
		return runtimecatalog.PromotionReceipt{}, fmt.Errorf("open relational storage: %w", err)
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	requests, err := factories.openPromotionRequests(ctx, store, db, catalogSettings(cfg).DeploymentID)
	if err != nil {
		return runtimecatalog.PromotionReceipt{}, err
	}
	return awaitPromotion(ctx, requests, request, wait, recorded)
}

// awaitPromotion writes the request and reads it until a leader settles it or the wait ends.
func awaitPromotion(
	ctx context.Context,
	requests promotionRequests,
	request runtimecatalog.PromotionRequest,
	wait time.Duration,
	recorded PromotionRecorded,
) (runtimecatalog.PromotionReceipt, error) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	record, err := requests.Submit(ctx, request)
	if err != nil {
		return runtimecatalog.PromotionReceipt{}, err
	}
	if record.Status == runtimecatalog.PromotionPending && recorded != nil {
		leader, err := requests.Leader(ctx)
		if err != nil {
			return runtimecatalog.PromotionReceipt{}, err
		}
		recorded(record, leader)
	}
	poll := time.NewTicker(promotionPollInterval)
	defer poll.Stop()
	for record.Status == runtimecatalog.PromotionPending {
		select {
		case <-ctx.Done():
			return runtimecatalog.PromotionReceipt{}, ctx.Err()
		case <-deadline.C:
			return runtimecatalog.PromotionReceipt{}, pendingPromotion(ctx, requests, record)
		case <-poll.C:
		}
		current, found, err := requests.Read(ctx, request.OperationID)
		if err != nil {
			return runtimecatalog.PromotionReceipt{}, err
		}
		if !found {
			// The request expired, or a later request replaced its outcome. The same
			// operation ID writes it again, and an applied operation returns its durable receipt.
			if current, err = requests.Submit(ctx, request); err != nil {
				return runtimecatalog.PromotionReceipt{}, err
			}
		}
		record = current
	}
	return *record.Receipt, nil
}

// pendingPromotion tells the operator how to continue the wait.
func pendingPromotion(ctx context.Context, requests promotionRequests, record runtimecatalog.PromotionRecord) error {
	err := fmt.Errorf("%w. Request %s stays recorded until %s. Run the command again with the same operation ID to continue the wait",
		ErrBaselinePromotionPending, record.OperationID, record.Expires.Format(time.RFC3339))
	if leader, leaderErr := requests.Leader(ctx); leaderErr == nil && leader == "" {
		err = fmt.Errorf("%w. No gateway leads the fleet. Start a gateway with the new binary", err)
	}
	return err
}

// CatalogBaselineStatus compares the packaged baseline of this binary with the retained fleet baseline.
// It reads shared storage without a write, never takes the publication lease, and replays the
// fleet head in a temporary directory instead of the gateway state directory.
func CatalogBaselineStatus(ctx context.Context, cfg *config.Config, options ...Option) (report runtimecatalog.BaselineReport, err error) {
	factories, err := baselineCommandFactories(cfg, options)
	if err != nil {
		return runtimecatalog.BaselineReport{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, catalogBaselineStatusTimeout)
	defer cancel()
	store, err := factories.openReadOnlyStorage(cfg.RuntimeStorage())
	if err != nil {
		return runtimecatalog.BaselineReport{}, fmt.Errorf("open storage: %w", err)
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	db, err := factories.openSQL(cfg.Storage)
	if err != nil {
		return runtimecatalog.BaselineReport{}, fmt.Errorf("open relational storage: %w", err)
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	observer, err := factories.openBaselineObserver(ctx, store, db, catalogSettings(cfg), runtimecatalog.DeploymentLookup(cfg.LookupDeployment))
	if err != nil {
		return runtimecatalog.BaselineReport{}, fmt.Errorf("open catalog: %w", err)
	}
	// Close runs under its own bound, so an expired report still removes the temporary directory.
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer closeCancel()
		err = errors.Join(err, observer.Close(closeCtx))
	}()
	return observer.BaselineReport()
}

// baselineCommandFactories selects the factories of a baseline command.
// A local deployment has no fleet baseline, so it refuses before it opens storage.
func baselineCommandFactories(cfg *config.Config, options []Option) (runtimeFactories, error) {
	if cfg == nil {
		return runtimeFactories{}, ErrConfigRequired
	}
	if cfg.Storage.Mode != storage.StorageTypeValkey || cfg.Storage.SQL.Mode != sqlstore.TypePostgres {
		return runtimeFactories{}, runtimecatalog.ErrBaselinePromotionFleetOnly
	}
	build := buildOptions{factories: defaultRuntimeFactories()}
	for _, option := range options {
		option(&build)
	}
	factories := build.factories
	if factories.openStorage == nil || factories.openReadOnlyStorage == nil || factories.openSQL == nil ||
		factories.openPromotionRequests == nil || factories.openBaselineObserver == nil {
		return runtimeFactories{}, errors.New("catalog baseline factories are incomplete")
	}
	return factories, nil
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
