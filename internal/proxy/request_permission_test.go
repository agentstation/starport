package proxy

import (
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

type cacheRequestPermission struct{ revoked bool }

func (p *cacheRequestPermission) Check() error {
	if p.revoked {
		return errors.New("withdrawn")
	}
	return nil
}

func TestCachedDeliveryRetainsRequestPermission(t *testing.T) {
	permission := &cacheRequestPermission{}
	ctx := inference.WithPermission(t.Context(), permission)
	require.Nil(t, cachePermissionFailure(ctx, nil))
	stream := newCachedEventStream(ctx, []inference.StreamEvent{{}}, time.Now(), nil)
	permission.revoked = true
	require.NotNil(t, cachePermissionFailure(ctx, nil))
	event, err := stream.Read()
	require.Error(t, err)
	require.Nil(t, event)
}
