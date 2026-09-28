package app

import (
	"context"
	"fmt"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/rs/zerolog/log"
)

const budgetSettlementTimeout = 5 * time.Second

type budgetOwner struct {
	ledger    *reservation.Repository
	admission *admission.Owner
	shared    *recovery.Authority
	recovery  *reservation.Recovery
}

// openBudgetAdmission binds required accounting to the durable storage authority.
// Shared startup reads independent approval. It cannot approve or reset history.
func (b *runtimeBuilder) openBudgetAdmission() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	owner := &budgetOwner{}
	var authority storage.TimeBoundStore
	if backend, shared := b.application.store.(storage.IncarnationProvider); shared {
		witness, err := recovery.New(b.sqlDB)
		if err != nil {
			return err
		}
		owner.shared, err = witness.OpenAuthority(ctx, backend, b.config.EffectivePaths().DeploymentID)
		if err != nil {
			return fmt.Errorf("open budget recovery authority: %w", err)
		}
		authority = owner.shared
	} else {
		var ok bool
		authority, ok = b.application.store.(storage.TimeBoundStore)
		if !ok {
			return fmt.Errorf("open budget authority: %w", reservation.ErrUnavailable)
		}
	}
	var err error
	owner.ledger, err = reservation.Open(authority)
	if err != nil {
		return err
	}
	owner.admission, err = admission.New(owner.ledger, budgetSettlementTimeout)
	if err != nil {
		return err
	}
	owner.recovery, err = reservation.NewRecovery(owner.ledger, b.application.store)
	if err != nil {
		return err
	}
	b.application.budget = owner
	return nil
}

// budgetRecoveryLoop retries retained usage independently of optional reports
// and job maintenance. Shutdown joins this worker before closing storage.
func (a *App) budgetRecoveryLoop(ctx context.Context) {
	for ctx.Err() == nil {
		passCtx, cancel := context.WithTimeout(ctx, budgetSettlementTimeout)
		result, err := a.budget.recovery.Pass(passCtx, 512)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// Storage errors can contain connection details. Log only counts.
			log.Warn().Int("scanned", result.Scanned).Int("recovered", result.Recovered).
				Int("failed", result.Failed).Int("held", result.Held).
				Msg("budget recovery could not finish; retained evidence will be retried")
		} else if result.Recovered > 0 {
			log.Info().Int("recovered", result.Recovered).Int("held", result.Held).
				Msg("budget recovery settled retained usage")
		}
		delay := time.Second
		if result.Complete {
			delay = 30 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// preparePolicy runs during bounded authorization loading, outside warm requests.
// The independent SQL grant can initialize only a newly created team's history.
func (o *budgetOwner) preparePolicy(ctx context.Context, teams reservation.TeamHistoryAuthority, policy *limits.BudgetPolicy) error {
	for index := range policy.RuleCount() {
		rule, _ := policy.Rule(index)
		if rule.Scope != limits.ScopeTeam {
			continue
		}
		if o.shared != nil {
			if err := o.shared.Check(ctx); err != nil {
				return err
			}
		}
		if err := o.ledger.InitializeTeamHistory(ctx, teams, rule.Holder, rule.Budget.Interval, rule.Budget.HistoryID); err != nil {
			return err
		}
	}
	return nil
}
