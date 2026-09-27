package limits

import "crypto/rand"

// WithBudgetHistory ignores caller-supplied history identifiers and preserves
// continuity only for a budget that keeps its original interval.
func (l *Limits) WithBudgetHistory(previous *Limits) *Limits {
	owned := l.Clone()
	if owned == nil {
		return nil
	}
	var spend, tokens *Budget
	if previous != nil {
		spend, tokens = previous.Spend, previous.Tokens
	}
	owned.Spend = prepareBudgetHistory(owned.Spend, spend)
	owned.Tokens = prepareBudgetHistory(owned.Tokens, tokens)
	return owned
}

func prepareBudgetHistory(current, previous *Budget) *Budget {
	if current == nil {
		return nil
	}
	owned := *current
	owned.HistoryID = rand.Text()
	if previous != nil && previous.Interval == current.Interval && previous.HistoryID != "" {
		owned.HistoryID = previous.HistoryID
	}
	return &owned
}

// WithBudgetHistory gives team policies the same continuity rules as other holders.
func (b *TeamBudget) WithBudgetHistory(previous *TeamBudget) *TeamBudget {
	if b == nil {
		return nil
	}
	var prior *Budget
	if previous != nil {
		value := Budget(*previous)
		prior = &value
	}
	owned := TeamBudget(*prepareBudgetHistory((*Budget)(b), prior))
	return &owned
}
