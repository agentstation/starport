package server

import (
	"context"
	"errors"
	"testing"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/stretchr/testify/require"
)

func TestBatchBudgetUnknownRequiredStateRefuses(t *testing.T) {
	budget := &limits.Limits{Spend: &limits.Budget{Limit: 10, Interval: limits.IntervalDay}}
	for _, tc := range []struct {
		name      string
		governor  batchGovernor
		admission controllers.BatchAdmission
	}{
		{name: "missing usage", admission: controllers.BatchAdmission{KeyLimits: budget}},
		{name: "unavailable usage", governor: batchGovernor{usage: stubUsageTotals{err: errors.New("unavailable")}}, admission: controllers.BatchAdmission{AccountLimits: budget}},
		{name: "missing team reader", admission: controllers.BatchAdmission{TeamID: "platform"}},
		{name: "unavailable team", governor: batchGovernor{teamBudget: func(context.Context, string) (*limits.TeamBudget, error) { return nil, errors.New("unavailable") }}, admission: controllers.BatchAdmission{TeamID: "platform"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.governor.admitBudget(t.Context(), tc.admission)
			var refused *failure.Failure
			require.ErrorAs(t, err, &refused)
			require.Equal(t, failure.GatewayUnavailable, refused.Kind())
		})
	}
}

func TestBatchBudgetConfirmedAbsenceNeedsNoUsageReader(t *testing.T) {
	governor := &batchGovernor{}
	require.NoError(t, governor.admitBudget(t.Context(), controllers.BatchAdmission{}))
}
