package recovery

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starport/internal/blob"
	"github.com/stretchr/testify/require"
)

func TestHistoryAssetsVerifyAndRecheckStreamedBytes(t *testing.T) {
	source, request, manifest := historyPackageFixture(t)
	require.NoError(t, os.Mkdir(filepath.Join(request.Directory, "assets"), 0o700))
	data := []byte("independently retained output")
	path := filepath.Join(request.Directory, "assets", "000001.bin")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	manifest.Assets = []historyAsset{{ID: "output", Path: "assets/000001.bin", Size: int64(len(data)), SHA256: historySHA256(data), Evidence: []string{"source"}}}
	request = writeHistoryManifest(t, request, manifest)
	verified, err := source.VerifyHistoryPackage(t.Context(), request)
	require.NoError(t, err)
	var output bytes.Buffer
	require.NoError(t, copyHistoryAsset(t.Context(), verified.state, "output", &output))
	require.Equal(t, data, output.Bytes())
	require.Error(t, copyHistoryAsset(t.Context(), verified.state, "unknown", io.Discard))
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, copyHistoryAsset(canceled, verified.state, "output", io.Discard), context.Canceled)
	require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte("x"), len(data)), 0o600))
	require.Error(t, copyHistoryAsset(t.Context(), verified.state, "output", io.Discard), "changed input never becomes accepted native output")
	_, err = source.VerifyHistoryPackage(t.Context(), request)
	require.Error(t, err)
}

func TestHistoryAssetsRefuseUnboundOrOversizedInputs(t *testing.T) {
	for name, change := range map[string]func(*historyManifest){
		"path":               func(m *historyManifest) { m.Assets[0].Path = "../external" },
		"digest":             func(m *historyManifest) { m.Assets[0].SHA256 = "unknown" },
		"negative":           func(m *historyManifest) { m.Assets[0].Size = -1 },
		"per-asset":          func(m *historyManifest) { m.Assets[0].Size = blob.ImportPublicationMaxBytes + 1 },
		"missing-evidence":   func(m *historyManifest) { m.Assets[0].Evidence = nil },
		"unknown-evidence":   func(m *historyManifest) { m.Assets[0].Evidence = []string{"absent"} },
		"duplicate-evidence": func(m *historyManifest) { m.Assets[0].Evidence = []string{"source", "source"} },
		"duplicate-id":       func(m *historyManifest) { m.Assets = append(m.Assets, m.Assets[0]) },
		"aggregate": func(m *historyManifest) {
			m.Assets = nil
			for i := range 17 {
				m.Assets = append(m.Assets, historyAsset{ID: fmt.Sprint(i), Path: fmt.Sprintf("assets/%06d.bin", i+1), Size: blob.ImportPublicationMaxBytes, SHA256: historySHA256(nil), Evidence: []string{"source"}})
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			source, request, manifest := historyPackageFixture(t)
			manifest.Assets = []historyAsset{{ID: "output", Path: "assets/000001.bin", Size: 0, SHA256: historySHA256(nil), Evidence: []string{"source"}}}
			change(&manifest)
			request = writeHistoryManifest(t, request, manifest)
			_, err := source.VerifyHistoryPackage(t.Context(), request)
			require.Error(t, err)
			require.NotErrorIs(t, err, os.ErrNotExist, "validate the manifest before opening asset files")
		})
	}
}
