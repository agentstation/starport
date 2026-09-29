package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func historyPackageFixture(t *testing.T) (*RestoreSource, HistoryPackageRequest, historyManifest) {
	t.Helper()
	root := privateKVDirectory(t)
	require.NoError(t, os.Mkdir(filepath.Join(root, "payloads"), 0o700))
	source := &RestoreSource{request: VerifyRequest{ManifestSHA256: strings.Repeat("a", 64)}, manifest: BundleManifest{Format: bundleFormat, Request: BundleRequest{Boundary: Record{DeploymentID: "deployment", Epoch: 7}}, FinishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}
	request := HistoryPackageRequest{Directory: root, Operation: RestoreOperation{ID: "restore-operation", FencingEvidence: "private-fence-reference"}, TargetSHA256: strings.Repeat("b", 64)}
	identity, err := source.ImportIdentity(request.Operation)
	require.NoError(t, err)
	encoded, err := json.Marshal(identity, json.Deterministic(true))
	require.NoError(t, err)
	sum := sha256.Sum256(encoded)
	manifest := historyManifest{Version: 1, BackupSHA256: source.request.ManifestSHA256, DeploymentID: "deployment", Operation: request.Operation, TargetSHA256: request.TargetSHA256, PreparedSHA256: hex.EncodeToString(sum[:]), Mode: "planned_migration", Disposition: "remain_restricted", Interval: historyInterval{Through: source.manifest.FinishedAt.Add(time.Hour), EndReference: "independent-cutoff"}, HighestEpoch: EpochEvidence{HighestEpoch: 9, SourceSHA256: strings.Repeat("c", 64), Reference: "independent-epoch", Operator: "operator"}, Evidence: []historyEvidence{{ID: "source", SHA256: strings.Repeat("c", 64), Size: 5, Reference: "private-history-reference"}}}
	return source, request, manifest
}

func writeHistoryManifest(t *testing.T, request HistoryPackageRequest, manifest historyManifest) HistoryPackageRequest {
	t.Helper()
	body, err := json.Marshal(manifest, json.Deterministic(true))
	require.NoError(t, err)
	return writeHistoryManifestBytes(t, request, body)
}

func writeHistoryManifestBytes(t *testing.T, request HistoryPackageRequest, body []byte) HistoryPackageRequest {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(request.Directory, "history.json"), body, 0o600))
	sum := sha256.Sum256(body)
	request.ManifestSHA256 = hex.EncodeToString(sum[:])
	return request
}

func TestHistoryPackageBindsSourceWithoutGrantingAuthority(t *testing.T) {
	source, request, manifest := historyPackageFixture(t)
	request = writeHistoryManifest(t, request, manifest)
	verified, err := source.VerifyHistoryPackage(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, request.ManifestSHA256, verified.Digest())
	require.Zero(t, verified.StepCount())
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%p", "%+p", "%#p"} {
		require.NotContains(t, fmt.Sprintf(format, *verified), "private-fence-reference")
		require.NotContains(t, fmt.Sprintf(format, *verified), "private-history-reference")
	}
	// Mutating caller values or retained files cannot change the verified private copy.
	manifest.Evidence[0].Reference = "changed"
	writeHistoryManifest(t, request, manifest)
	require.Equal(t, "private-history-reference", verified.state.manifest.Evidence[0].Reference)
	_, err = source.VerifyHistoryPackage(t.Context(), request)
	require.Error(t, err)
}

func TestHistoryPackageRefusesInvalidMetadata(t *testing.T) {
	for name, change := range map[string]func(*historyManifest){
		"version":            func(m *historyManifest) { m.Version++ },
		"backup":             func(m *historyManifest) { m.BackupSHA256 = strings.Repeat("d", 64) },
		"deployment":         func(m *historyManifest) { m.DeploymentID = "another" },
		"operation":          func(m *historyManifest) { m.Operation.ID = "another" },
		"fence":              func(m *historyManifest) { m.Operation.FencingEvidence = "another" },
		"target":             func(m *historyManifest) { m.TargetSHA256 = strings.Repeat("d", 64) },
		"prepared":           func(m *historyManifest) { m.PreparedSHA256 = strings.Repeat("d", 64) },
		"mode":               func(m *historyManifest) { m.Mode = "automatic" },
		"disposition":        func(m *historyManifest) { m.Disposition = "activate" },
		"interval":           func(m *historyManifest) { m.Interval.Through = time.Time{} },
		"missing-evidence":   func(m *historyManifest) { m.Evidence = nil },
		"duplicate-evidence": func(m *historyManifest) { m.Evidence = append(m.Evidence, m.Evidence[0]) },
		"epoch":              func(m *historyManifest) { m.HighestEpoch.HighestEpoch = 6 },
		"unsafe-reference":   func(m *historyManifest) { m.Evidence[0].Reference = "unsafe\nreference" },
		"unknown-kind": func(m *historyManifest) {
			m.Steps = []historyStep{{Ordinal: 1, Kind: "raw_sql", Path: "payloads/000001.json", Size: 2, SHA256: strings.Repeat("e", 64), Evidence: []string{"source"}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			source, request, manifest := historyPackageFixture(t)
			change(&manifest)
			request = writeHistoryManifest(t, request, manifest)
			verified, err := source.VerifyHistoryPackage(t.Context(), request)
			require.Error(t, err)
			require.Nil(t, verified)
		})
	}
}

func TestHistoryPackageRefusesUnknownAndDuplicateMembers(t *testing.T) {
	for _, extra := range []string{`,"unknown":true}`, `,"version":1}`} {
		source, request, manifest := historyPackageFixture(t)
		body, err := json.Marshal(manifest)
		require.NoError(t, err)
		body = append(body[:len(body)-1], extra...)
		request = writeHistoryManifestBytes(t, request, body)
		_, err = source.VerifyHistoryPackage(t.Context(), request)
		require.Error(t, err)
	}
}

func TestHistoryPackageRefusesCancellationAndMissingSource(t *testing.T) {
	source, request, manifest := historyPackageFixture(t)
	request = writeHistoryManifest(t, request, manifest)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := source.VerifyHistoryPackage(ctx, request)
	require.ErrorIs(t, err, context.Canceled)
	var missing *RestoreSource
	_, err = missing.VerifyHistoryPackage(t.Context(), request)
	require.Error(t, err)
}

func TestHistoryPackageCopiesExactPayloads(t *testing.T) {
	source, request, manifest := historyPackageFixture(t)
	body := []byte(`{"private":"retained-private-history"}`)
	manifest.Steps = []historyStep{{Ordinal: 1, Kind: "sql_identity", Path: "payloads/000001.json", Size: len(body), SHA256: historySHA256(body), Evidence: []string{"source"}}}
	path := filepath.Join(request.Directory, "payloads", "000001.json")
	require.NoError(t, os.WriteFile(path, body, 0o600))
	request = writeHistoryManifest(t, request, manifest)
	verified, err := source.VerifyHistoryPackage(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, 1, verified.StepCount())
	require.NoError(t, os.WriteFile(path, []byte(`{"changed":true}`), 0o600))
	require.Equal(t, body, verified.state.payloads[0])
	_, err = source.VerifyHistoryPackage(t.Context(), request)
	require.Error(t, err)
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%p", "%+p", "%#p"} {
		require.NotContains(t, fmt.Sprintf(format, *verified), "retained-private-history")
	}
}

func TestHistoryPackageRefusesPayloadMetadataBeforeReading(t *testing.T) {
	for name, change := range map[string]func(*historyManifest){
		"ordinal":          func(m *historyManifest) { m.Steps[0].Ordinal = 2 },
		"traversal":        func(m *historyManifest) { m.Steps[0].Path = "../other.json" },
		"source":           func(m *historyManifest) { m.Steps[0].Evidence = []string{"missing"} },
		"duplicate-source": func(m *historyManifest) { m.Steps[0].Evidence = []string{"source", "source"} },
		"empty-size":       func(m *historyManifest) { m.Steps[0].Size = 0 },
		"payload-limit":    func(m *historyManifest) { m.Steps[0].Size = historyPayloadMaxBytes + 1 },
		"aggregate-limit": func(m *historyManifest) {
			m.Steps = nil
			for i := range 9 {
				m.Steps = append(m.Steps, historyStep{Ordinal: i + 1, Kind: "sql_identity", Path: fmt.Sprintf("payloads/%06d.json", i+1), Size: historyPayloadMaxBytes, SHA256: strings.Repeat("e", 64), Evidence: []string{"source"}})
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			source, request, manifest := historyPackageFixture(t)
			manifest.Steps = []historyStep{{Ordinal: 1, Kind: "sql_identity", Path: "payloads/000001.json", Size: 2, SHA256: historySHA256([]byte("{}")), Evidence: []string{"source"}}}
			change(&manifest)
			request = writeHistoryManifest(t, request, manifest)
			_, err := source.VerifyHistoryPackage(t.Context(), request)
			require.Error(t, err)
			require.NotErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestHistoryPackageRefusesPayloadDigestAndSyntax(t *testing.T) {
	for name, body := range map[string][]byte{"digest": []byte(`{"valid":true}`), "syntax": []byte(`{"invalid"`), "duplicate": []byte(`{"v":1,"v":2}`)} {
		t.Run(name, func(t *testing.T) {
			source, request, manifest := historyPackageFixture(t)
			digest := historySHA256(body)
			if name == "digest" {
				digest = strings.Repeat("e", 64)
			}
			manifest.Steps = []historyStep{{Ordinal: 1, Kind: "sql_identity", Path: "payloads/000001.json", Size: len(body), SHA256: digest, Evidence: []string{"source"}}}
			require.NoError(t, os.WriteFile(filepath.Join(request.Directory, "payloads", "000001.json"), body, 0o600))
			request = writeHistoryManifest(t, request, manifest)
			_, err := source.VerifyHistoryPackage(t.Context(), request)
			require.Error(t, err)
		})
	}
}

func TestHistoryPackageRefusesOversizedManifest(t *testing.T) {
	source, request, manifest := historyPackageFixture(t)
	body, err := json.Marshal(manifest)
	require.NoError(t, err)
	body = append(body, []byte(strings.Repeat(" ", historyManifestMaxBytes))...)
	request = writeHistoryManifestBytes(t, request, body)
	_, err = source.VerifyHistoryPackage(t.Context(), request)
	require.Error(t, err)
}
