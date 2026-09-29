package storage

import (
	"context"
	"errors"
	"strings"

	"github.com/agentstation/starport/internal/deployment"
)

// OpenUnprefixedValkeySnapshotSource captures a dedicated database for explicit
// namespace migration. The operator must stop and fence every source writer.
// The connection exposes enumeration only and binds the observed process identity.
// Ordinary gateway storage never selects this layout.
func OpenUnprefixedValkeySnapshotSource(ctx context.Context, config ValkeyConfig) (SnapshotSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := deployment.ValidateID(config.DeploymentID); err != nil {
		return nil, err
	}
	store, err := openValkey(config, "")
	if err != nil {
		return nil, err
	}
	source, err := snapshotSourceForStore(ctx, store)
	if err != nil {
		return nil, err
	}
	return &unprefixedSnapshotSource{source}, nil
}

type unprefixedSnapshotSource struct{ SnapshotSource }

func (s *unprefixedSnapshotSource) Enumerate(ctx context.Context, yield func(TransferRecord) error) error {
	if yield == nil {
		return ErrInvalidMutation
	}
	return s.SnapshotSource.Enumerate(ctx, func(record TransferRecord) error {
		if strings.HasPrefix(record.Key, "{starport:") {
			return errors.New("unprefixed capture requires a dedicated source database without deployment namespaces")
		}
		return yield(record)
	})
}
