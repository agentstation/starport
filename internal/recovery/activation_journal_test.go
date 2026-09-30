package recovery

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActivationJournalPreservesOriginalDecisionAndPhaseOrder(t *testing.T) {
	path := t.TempDir()
	require.NoError(t, os.Chmod(path, 0o700))
	original := []byte(`{"version":1,"original":"retained evidence"}`)
	journal, err := SealActivationJournal(t.Context(), path, original)
	require.NoError(t, err)
	require.Error(t, journal.PublishPhase(t.Context(), ActivationKV))
	for n, phase := range activationPhases {
		require.NoError(t, journal.PublishPhase(t.Context(), phase))
		reopened, err := InspectActivationJournal(t.Context(), path, journal.Digest())
		require.NoError(t, err)
		require.Equal(t, n+1, reopened.CompletedPhases())
		require.NoError(t, reopened.PublishPhase(t.Context(), phase))
		body, err := reopened.Record()
		require.NoError(t, err)
		require.Equal(t, original, body)
	}
	_, err = SealActivationJournal(t.Context(), path, []byte(`{"version":1,"original":"replacement"}`))
	require.Error(t, err)
	require.NoError(t, os.Remove(filepath.Join(path, "blobs.json")))
	_, err = InspectActivationJournal(t.Context(), path, journal.Digest())
	require.Error(t, err)
}

func TestActivationJournalRejectsSubstitutedAndUnexpectedEvidence(t *testing.T) {
	for _, name := range []string{"decision", "phase", "omitted", "unexpected", "digest"} {
		t.Run(name, func(t *testing.T) {
			path := t.TempDir()
			require.NoError(t, os.Chmod(path, 0o700))
			j, err := SealActivationJournal(t.Context(), path, []byte(`{"version":1}`))
			require.NoError(t, err)
			require.NoError(t, j.PublishPhase(t.Context(), ActivationBlobs))
			digest := j.Digest()
			switch name {
			case "decision":
				require.NoError(t, os.WriteFile(filepath.Join(path, "decision.json"), []byte(`{"version":2}`), 0o600))
			case "phase":
				require.NoError(t, os.WriteFile(filepath.Join(path, "blobs.json"), []byte(`{"phase":"sql"}`), 0o600))
			case "omitted":
				require.NoError(t, os.Remove(filepath.Join(path, "decision.json")))
			case "unexpected":
				require.NoError(t, os.WriteFile(filepath.Join(path, "unknown.json"), []byte(`{}`), 0o600))
			case "digest":
				digest = strings.Repeat("a", 64)
			}
			_, err = InspectActivationJournal(t.Context(), path, digest)
			require.Error(t, err)
		})
	}
	var empty ActivationJournal
	_, err := empty.Record()
	require.Error(t, err)
	require.Error(t, empty.PublishPhase(t.Context(), ActivationBlobs))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = SealActivationJournal(ctx, t.TempDir(), []byte(`{}`))
	require.Error(t, err)
}

func TestActivationJournalFreshProcessReopensOriginalPhases(t *testing.T) {
	if path := os.Getenv("STARPORT_TEST_ACTIVATION_JOURNAL"); path != "" {
		journal, err := InspectActivationJournal(t.Context(), path, os.Getenv("STARPORT_TEST_ACTIVATION_DIGEST"))
		require.NoError(t, err)
		expected, err := strconv.Atoi(os.Getenv("STARPORT_TEST_ACTIVATION_PHASES"))
		require.NoError(t, err)
		require.Equal(t, expected, journal.CompletedPhases())
		original, err := journal.Record()
		require.NoError(t, err)
		require.Equal(t, []byte(`{"version":1,"original":"fresh-process-evidence"}`), original)
		return
	}
	path := t.TempDir()
	require.NoError(t, os.Chmod(path, 0o700))
	journal, err := SealActivationJournal(t.Context(), path, []byte(`{"version":1,"original":"fresh-process-evidence"}`))
	require.NoError(t, err)
	for count := 0; count <= len(activationPhases); count++ {
		if count > 0 {
			require.NoError(t, journal.PublishPhase(t.Context(), activationPhases[count-1]))
		}
		command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestActivationJournalFreshProcessReopensOriginalPhases$")
		command.Env = append(os.Environ(), "STARPORT_TEST_ACTIVATION_JOURNAL="+path, "STARPORT_TEST_ACTIVATION_DIGEST="+journal.Digest(), "STARPORT_TEST_ACTIVATION_PHASES="+strconv.Itoa(count))
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
	}
}
