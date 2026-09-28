package config

import "errors"

// ErrBudgetAdmissionMode reports an unsupported capacity authority.
var ErrBudgetAdmissionMode = errors.New("budget admission supports only atomic mode. Local quota leases and cached balances are unsupported")

// BudgetAdmissionConfig selects the capacity authority for required budgets.
type BudgetAdmissionConfig struct {
	Mode string `env:"MODE,default=atomic"`
}

// Validate refuses modes without a qualified capacity and recovery contract.
// An omitted value in direct construction uses the same atomic admission.
func (c BudgetAdmissionConfig) Validate() error {
	if c.Mode != "" && c.Mode != "atomic" {
		return ErrBudgetAdmissionMode
	}
	return nil
}
