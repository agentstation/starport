package app

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/diagnosis"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

func TestRecoveryActivationLocalOperatorCommands(t *testing.T) {
	cfg, prepare := boundedActivationSourceFixture(t)
	cfg, request := activationPreparedFixture(t, cfg, prepare)
	parent := filepath.Join(t.TempDir(), "private-request")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	body, err := json.Marshal(request)
	require.NoError(t, err)
	file := filepath.Join(parent, "activation.json")
	require.NoError(t, os.WriteFile(file, body, 0600))
	var output bytes.Buffer
	deps := cli.Dependencies{
		Stdout: &output, Stderr: &output, Stdin: bytes.NewReader(nil),
		LoadConfig:       func(context.Context) (*config.Config, error) { return cfg, nil },
		ActivateRecovery: ActivateRecovery, InspectRecoveryActivation: InspectRecoveryActivation,
		ResolvePaths: func() (config.Paths, error) { return cfg.EffectivePaths(), nil },
		RunServer: func(context.Context, cli.GatewayOptions) error {
			t.Fatal("recovery started a gateway")
			return nil
		},
		StartDevelopment: func(context.Context, cli.GatewayOptions) (cli.DevelopmentSession, error) {
			t.Fatal("recovery started development")
			return cli.DevelopmentSession{}, nil
		},
		Initialize: func(context.Context, cli.InitOptions) (cli.InitResult, error) {
			t.Fatal("recovery initialized a gateway")
			return cli.InitResult{}, nil
		},
		Diagnose: func(context.Context, diagnosis.Options) diagnosis.Report {
			t.Fatal("recovery ran diagnostics")
			return diagnosis.Report{}
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	require.NoError(t, cli.Run(ctx, []string{"starport", "backup", "activate", "--request-file", file, "--json"}, deps))
	var completed recovery.ActivationResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &completed))
	require.True(t, completed.HistoricallyComplete)
	require.True(t, completed.CurrentAdmissionValid)
	require.False(t, completed.Restricted)
	require.Equal(t, 3, completed.CompletedPhases)
	require.Len(t, completed.DecisionSHA256, 64)
	output.Reset()
	require.NoError(t, cli.Run(ctx, []string{"starport", "backup", "activation-status", "--request-file", file, "--decision-sha256", completed.DecisionSHA256, "--json"}, deps))
	var status recovery.ActivationResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &status))
	require.Equal(t, completed, status)
	output.Reset()
	require.NoError(t, cli.Run(ctx, []string{"starport", "backup", "activate", "--request-file", file, "--decision-sha256", completed.DecisionSHA256, "--json"}, deps))
	var repeated recovery.ActivationResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &repeated))
	require.Equal(t, completed, repeated)
}
