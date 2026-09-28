package storage

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go/mock"
	"go.uber.org/mock/gomock"
)

func TestValkeyScanPageKeepsEmptyContinuationAndOversizedResponse(t *testing.T) {
	client := mock.NewClient(gomock.NewController(t))
	store := &ValkeyStore{client: client, prefix: "deployment:"}
	client.EXPECT().Do(boundedCommandContext(), mock.Match("SCAN", "0", "MATCH", "deployment:jobs:*", "COUNT", "1")).Return(
		mock.Result(mock.ValkeyArray(mock.ValkeyString("5"), mock.ValkeyArray())))
	page, err := store.ScanPage(t.Context(), "jobs:", "", 1)
	require.NoError(t, err)
	require.Empty(t, page.Keys)
	require.Equal(t, "5", page.Next)
	client.EXPECT().Do(boundedCommandContext(), mock.Match("SCAN", "5", "MATCH", "deployment:jobs:*", "COUNT", "1")).Return(
		mock.Result(mock.ValkeyArray(mock.ValkeyString("0"), mock.ValkeyArray(mock.ValkeyString("deployment:jobs:1"), mock.ValkeyString("deployment:jobs:2")))))
	page, err = store.ScanPage(t.Context(), "jobs:", page.Next, 1)
	require.NoError(t, err)
	require.Equal(t, []string{"jobs:1", "jobs:2"}, page.Keys)
	require.Empty(t, page.Next)
}
