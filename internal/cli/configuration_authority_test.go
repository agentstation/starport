package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/configrevision"
	"github.com/stretchr/testify/require"
)

func TestConfigAuthorityCommandsAreDiscoverable(t *testing.T) {
	for _, command := range []string{"init", "migrate", "apply", "effective"} {
		deps, output, _ := testDependencies()
		require.NoError(t, Run(t.Context(), []string{"starport", "config", command, "--help"}, deps))
		require.Contains(t, output.String(), "--json", command)
	}
}

func TestConfigInitSharedPreviewsUntilConfirmed(t *testing.T) {
	for _, mode := range []string{"preview", "write", "missing-shared", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			deps, output, _ := testDependencies()
			calls := 0
			failure := &configrevision.UnavailableError{Err: errors.New("connection refused")}
			deps.InitializeSharedConfiguration = func(_ context.Context, cfg *config.Config, request configrevision.Request) (configrevision.Result, error) {
				calls++
				require.NotNil(t, cfg)
				require.Equal(t, "first", request.OperationID)
				if mode == "unavailable" {
					return configrevision.Result{}, failure
				}
				revision := configrevision.Revision{Head: configrevision.Head{Namespace: "blue", Sequence: 1, Authority: configrevision.AuthorityShared, Checksum: "abc"}, OperationID: "first"}
				return configrevision.Result{Revision: revision, Written: !request.Preview}, nil
			}
			args := []string{"starport", "config", "init", "--shared", "--operation", "first"}
			switch mode {
			case "write":
				args = append(args, "--yes")
			case "missing-shared":
				args = []string{"starport", "config", "init", "--yes", "--operation", "first"}
			}
			err := Run(t.Context(), args, deps)
			switch mode {
			case "preview":
				require.NoError(t, err)
				require.Contains(t, output.String(), "Preview: shared configuration revision 1 in namespace blue")
				require.Contains(t, output.String(), "starport config init --shared --yes")
			case "write":
				require.NoError(t, err)
				require.Contains(t, output.String(), "Wrote shared configuration revision 1 in namespace blue")
				require.Contains(t, output.String(), "Operation: first")
			case "unavailable":
				require.ErrorAs(t, err, &failure)
				require.Equal(t, ExitCodeRuntime, ExitCode(err))
			default:
				require.Error(t, err)
				require.Zero(t, calls)
				require.Equal(t, ExitCodeUsage, ExitCode(err))
			}
		})
	}
}

func TestConfigMigrateToLocalRequiresConfirmation(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		target string
	}{
		{name: "local unconfirmed", args: []string{"--to", "local"}},
		{name: "unknown target", args: []string{"--to", "external", "--yes"}},
		{name: "local confirmed", args: []string{"--to", "local", "--yes"}, target: configrevision.AuthorityLocal},
		{name: "shared", args: []string{"--to", "shared"}, target: configrevision.AuthorityShared},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, output, _ := testDependencies()
			calls := 0
			deps.MigrateConfiguration = func(_ context.Context, _ *config.Config, target string, request configrevision.Request) (configrevision.Result, error) {
				calls++
				require.Equal(t, tc.target, target)
				require.False(t, request.Preview)
				return configrevision.Result{Revision: configrevision.Revision{Head: configrevision.Head{Sequence: 4, Authority: target}}, Written: true}, nil
			}
			err := Run(t.Context(), append([]string{"starport", "config", "migrate"}, tc.args...), deps)
			if tc.target == "" {
				require.Error(t, err)
				require.Equal(t, ExitCodeUsage, ExitCode(err))
				require.Zero(t, calls)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Contains(t, output.String(), "STARPORT_CONFIG_MANAGEMENT="+tc.target)
		})
	}
}

func TestConfigApplyResumeRepeatsTheNamedOperation(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		request configrevision.ApplyRequest
		usage   bool
	}{
		{name: "apply", args: []string{"--operation", "apply-two"}, request: configrevision.ApplyRequest{OperationID: "apply-two"}},
		{name: "resume", args: []string{"--resume", "apply-two", "--json"}, request: configrevision.ApplyRequest{OperationID: "apply-two", Resume: true}},
		{name: "resume with operation", args: []string{"--resume", "apply-two", "--operation", "apply-three"}, usage: true},
		{name: "empty resume", args: []string{"--resume", " "}, usage: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, output, _ := testDependencies()
			calls := 0
			deps.ApplyConfiguration = func(_ context.Context, _ *config.Config, request configrevision.ApplyRequest) (configrevision.Result, error) {
				calls++
				require.Equal(t, tc.request, request)
				revision := configrevision.Revision{Head: configrevision.Head{Sequence: 2, Authority: configrevision.AuthorityShared}, OperationID: request.OperationID}
				return configrevision.Result{Revision: revision, Written: !request.Resume, Fence: configrevision.FenceApplied}, nil
			}
			err := Run(t.Context(), append([]string{"starport", "config", "apply"}, tc.args...), deps)
			if tc.usage {
				require.Error(t, err)
				require.Equal(t, ExitCodeUsage, ExitCode(err))
				require.Zero(t, calls)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			if tc.request.Resume {
				require.Contains(t, output.String(), `"fence": "applied"`)
				require.Contains(t, output.String(), `"written": false`)
				return
			}
			require.Contains(t, output.String(), "The fleet policy names revision 2")
		})
	}
}

func TestConfigEffectiveReportsAuthorityAndIgnoredValues(t *testing.T) {
	deps, output, _ := testDependencies()
	deps.EffectiveConfiguration = func(context.Context, *config.Config) (config.EffectiveReport, error) {
		return config.EffectiveReport{
			Management: config.ManagementShared, Namespace: "blue",
			Revision: config.AppliedRevision{Authority: config.ManagementShared, Desired: 3, Applied: 2, Checksum: "abc"},
			Settings: []config.EffectiveSetting{{
				Name: "STARMAP_CATALOG_SOURCE_POLL_INTERVAL", Value: "30s", Scope: "deployment", Authority: config.ManagementShared,
				Origin: "shared-revision", Ignored: []config.IgnoredValue{{Origin: "environment", Reason: "shared-authority"}},
			}},
		}, nil
	}
	require.NoError(t, Run(t.Context(), []string{"starport", "config", "effective"}, deps))
	require.Contains(t, output.String(), "Management: shared")
	require.Contains(t, output.String(), "Revision: desired 3, applied 2, checksum abc")
	require.Contains(t, output.String(), `STARMAP_CATALOG_SOURCE_POLL_INTERVAL="30s" authority=shared`)
	require.Contains(t, output.String(), "ignored environment: shared-authority")
}
