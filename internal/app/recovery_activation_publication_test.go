package app

import (
	"context"
	"encoding/json/v2"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

// The overlay adds one producer-owned test probe without changing module bytes or production hooks.
func buildActivationPublicationProbe(t *testing.T) string {
	t.Helper()
	return buildActivationProducerProbe(t, "internal/privatefiles", "probes/publication/activation_publication_probe_test.go")
}

func buildActivationProducerProbe(t *testing.T, owner, source string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "go", "list", "-m", "-json", "github.com/agentstation/starmap")
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.Output()
	require.NoError(t, err)
	var module struct{ Dir, Version, GoVersion, Sum string }
	require.NoError(t, json.Unmarshal(output, &module))
	require.True(t, strings.HasPrefix(module.Version, "v"))
	require.True(t, filepath.IsAbs(module.Dir))
	require.Equal(t, "1.27.1", module.GoVersion)
	require.True(t, strings.HasPrefix(module.Sum, "h1:"))
	ownedModule := filepath.Join(t.TempDir(), "producer")
	copyActivationProbeModule(t, module.Dir, ownedModule)
	module.Dir, err = filepath.EvalSymlinks(ownedModule)
	require.NoError(t, err)
	probe, err := filepath.Abs(filepath.Join("testdata", source))
	require.NoError(t, err)
	probeTarget := filepath.Join(module.Dir, owner, "starport_activation_owner_probe_test.go")
	require.NoError(t, os.WriteFile(probeTarget, []byte("package "+filepath.Base(owner)+"\n"), 0600))
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{probeTarget: probe}})
	require.NoError(t, err)
	directory := t.TempDir()
	overlayPath := filepath.Join(directory, "overlay.json")
	require.NoError(t, os.WriteFile(overlayPath, overlay, 0600))
	binary := filepath.Join(directory, "publication-probe")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	command = exec.CommandContext(t.Context(), "go", "test", "-c", "-overlay", overlayPath, "-o", binary, "./"+owner)
	command.Dir = module.Dir
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err = command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	return binary
}

func crashActivationPhasePublication(t *testing.T, binary string, cfg *config.Config, sealed *sealedRecoveryActivation, native *recoveryActivationNative, count int, crash string) {
	t.Helper()
	phase := []recovery.ActivationPhase{recovery.ActivationBlobs, recovery.ActivationKV, recovery.ActivationSQL}[count-1]
	commitActivationNativePhases(t, cfg, sealed, native, count)
	// Read original phase bytes from the journal owner, then inject a lost publication in its native owner.
	require.NoError(t, sealed.journal.PublishPhase(t.Context(), phase))
	name := string(phase) + ".json"
	phasePath := filepath.Join(sealed.request.ActivationDirectory, name)
	body, err := os.ReadFile(phasePath)
	require.NoError(t, err)
	require.NoError(t, os.Remove(phasePath))
	runActivationPublicationProbe(t, binary, sealed.request.ActivationDirectory, name, body, crash)

}

func TestRecoveryActivationExactPendingPhaseRestart(t *testing.T) {
	binary := buildActivationPublicationProbe(t)
	for count := 1; count <= 3; count++ {
		for _, crash := range []string{"header", "empty", "prepared", "published"} {
			t.Run(string([]recovery.ActivationPhase{recovery.ActivationBlobs, recovery.ActivationKV, recovery.ActivationSQL}[count-1])+"/"+crash, func(t *testing.T) {
				cfg, request := activationFleetFixture(t)
				sealed, native := sealActivationFixture(t, cfg, request)
				request.ExpectedDecisionSHA256 = sealed.journal.Digest()
				crashActivationPhasePublication(t, binary, cfg, sealed, native, count, crash)
				require.NoError(t, native.close())
				runActivationChild(t, cfg, request)
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()
				result, err := InspectRecoveryActivation(ctx, cfg, request)
				require.NoError(t, err)
				require.True(t, result.HistoricallyComplete)
			})
		}
	}
}

func copyActivationProbeModule(t *testing.T, source, destination string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !entry.Type().IsRegular() {
			return fs.ErrInvalid
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, 0600)
	}))
}

func runActivationPublicationProbe(t *testing.T, binary, directory, name string, body []byte, crash string) {
	t.Helper()
	fixture, err := json.Marshal(struct {
		Directory string
		Name      string
		Data      []byte
		Crash     string
	}{directory, name, body, crash})
	require.NoError(t, err)
	fixturePath := filepath.Join(t.TempDir(), "publication-fixture.json")
	require.NoError(t, os.WriteFile(fixturePath, fixture, 0600))
	command := exec.CommandContext(t.Context(), binary, "-test.run=^TestStarportActivationPublicationProbe$")
	command.Env = append(os.Environ(), "STARPORT_PUBLICATION_PROBE_FIXTURE="+fixturePath)
	output, _ := command.CombinedOutput()
	require.NotNil(t, command.ProcessState, "%s", output)
	require.Equal(t, 88, command.ProcessState.ExitCode(), "%s", output)
}

// This contract proves exact app journal continuation through the actual producer owner process exit.
// Native component authorization has separate complete application tests.
func TestRecoveryActivationJournalExactPublicationProcessExit(t *testing.T) {
	binary := buildActivationPublicationProbe(t)
	for index, phase := range []recovery.ActivationPhase{recovery.ActivationBlobs, recovery.ActivationKV, recovery.ActivationSQL} {
		for _, crash := range []string{"header", "empty", "prepared", "published", "foreign", "wrong-prefix", "wrong-bytes", "unowned", "incomplete"} {
			t.Run(string(phase)+"/"+crash, func(t *testing.T) {
				directory := t.TempDir()
				require.NoError(t, os.Chmod(directory, 0700))
				original := []byte(`{"version":1,"original":"fixed journal evidence"}`)
				journal, err := recovery.SealActivationJournal(t.Context(), directory, original)
				require.NoError(t, err)
				phases := []recovery.ActivationPhase{recovery.ActivationBlobs, recovery.ActivationKV, recovery.ActivationSQL}
				for preceding := 0; preceding <= index; preceding++ {
					require.NoError(t, journal.PublishPhase(t.Context(), phases[preceding]))
				}
				name := string(phase) + ".json"
				path := filepath.Join(directory, name)
				body, err := os.ReadFile(path)
				require.NoError(t, err)
				require.NoError(t, os.Remove(path))
				runActivationPublicationProbe(t, binary, directory, name, body, crash)
				_, err = recovery.InspectActivationJournal(t.Context(), directory, journal.Digest())
				require.Error(t, err, "passive regular inspection must preserve pending publication evidence")
				pending, err := recovery.InspectActivationPhaseCompletion(t.Context(), directory, journal.Digest(), phase)
				refused := crash == "foreign" || crash == "wrong-prefix" || crash == "wrong-bytes" || crash == "unowned" || crash == "incomplete"
				if refused {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				actual, err := pending.Record()
				require.NoError(t, err)
				require.Equal(t, original, actual)
				calls := 0
				require.NoError(t, pending.CompletePhase(t.Context(), phase, func(context.Context) error { calls++; return nil }))
				require.Positive(t, calls)
				checked, err := recovery.InspectActivationJournal(t.Context(), directory, journal.Digest())
				require.NoError(t, err)
				require.Equal(t, index+1, checked.CompletedPhases())
				actual, err = checked.Record()
				require.NoError(t, err)
				require.Equal(t, original, actual)
			})
		}
	}
}
