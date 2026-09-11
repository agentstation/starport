package catalog

import (
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/failure"
)

// catalogAttemptPermission checks current permission for one accepted authority head.
// Implementations must read only memory and support concurrent calls.
type catalogAttemptPermission interface {
	AllowsCatalogAttempt(catalogs.CatalogAuthorityHead) bool
}

// acceptedCatalogSource combines durable accepted metadata with current runtime permission.
type acceptedCatalogSource struct {
	Source
	catalogAttemptPermission
}

// AllowsNewAttempt checks current permission for this snapshot without storage access.
// Call it before route selection, each provider attempt, and cached response delivery.
// Retained metadata alone cannot authorize an authoritative catalog.
func (s *RoutableSnapshot) AllowsNewAttempt() bool {
	if s == nil || s.catalog == nil {
		return false
	}
	if s.permission != nil {
		return s.permission.AllowsCatalogAttempt(s.authorityHead)
	}
	return s.authorityHead == (catalogs.CatalogAuthorityHead{})
}

// CheckNewAttempt returns a retryable gateway refusal when current permission is unavailable.
// A refusal carries no provider-health evidence. Successful checks allocate no memory.
func (s *RoutableSnapshot) CheckNewAttempt() *failure.Failure {
	if s.AllowsNewAttempt() {
		return nil
	}
	return failure.New(failure.GatewayUnavailable, "Catalog permission is unavailable.", true, failure.ProviderDetails{}, nil)
}
