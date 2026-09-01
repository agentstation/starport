package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/agentstation/starport/internal/catalog/view"
	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/diagnosis"
)

// runProcessCommand executes the command tree with freshly built extra
// commands and noop runtime boundaries, mirroring runContext's error
// handling. Commands are built per run because a parsed urfave command
// carries state.
func runProcessCommand(
	t *testing.T,
	build func() []*urfavecli.Command,
	args ...string,
) (string, string, int) {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	err := starportcli.Run(context.Background(), args, starportcli.Dependencies{
		Stdin: bytes.NewReader(nil), Stdout: stdout, Stderr: stderr,
		RunServer:        func(context.Context, starportcli.GatewayOptions) error { return nil },
		StartDevelopment: noopDevelopmentStarter,
		Initialize:       noopInitializer,
		LoadConfig: func(context.Context) (*config.Config, error) {
			return nil, errors.New("configuration is not part of this test")
		},
		ResolvePaths: func() (config.Paths, error) { return config.Paths{}, nil },
		Diagnose: func(context.Context, diagnosis.Options) diagnosis.Report {
			return diagnosis.Report{}
		},
		ExtraCommands: build(),
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
	}
	return stdout.String(), stderr.String(), starportcli.ExitCode(err)
}

func staticModelsCommands(t *testing.T) func() []*urfavecli.Command {
	t.Helper()
	return func() []*urfavecli.Command {
		return []*urfavecli.Command{newModelsCommand(staticModelLoader(t))}
	}
}

func intValue(value int) *int { return &value }

func staticCatalog() []view.ModelInfo {
	return []view.ModelInfo{
		{
			ID: "openai/gpt-4o-mini", Name: "GPT-4o mini", OwnedBy: "openai",
			Context: intValue(128000),
			Pricing: &view.ModelPricing{Prompt: "1.5e-07", Completion: "6e-07", Currency: "USD"},
			Offerings: []view.ModelOfferingInfo{{
				Provider: "openai", ProviderModelID: "gpt-4o-mini",
				Operations: []string{"chat-completions"},
			}},
		},
		{
			ID: "openai/gpt-4o", Name: "GPT-4o", OwnedBy: "openai",
			Context: intValue(128000),
			Pricing: &view.ModelPricing{Prompt: "2.5e-06", Completion: "1e-05", Currency: "USD"},
		},
		{
			ID: "anthropic/claude-sonnet-4", Name: "Claude Sonnet 4", OwnedBy: "anthropic",
			Authors: []view.ModelAuthorInfo{{ID: "anthropic", Name: "Anthropic"}},
		},
	}
}

func staticModelLoader(t *testing.T) catalogModels {
	t.Helper()
	return func(context.Context) ([]view.ModelInfo, error) {
		return staticCatalog(), nil
	}
}

func TestModelsSearchAnswersJSON(t *testing.T) {
	commands := staticModelsCommands(t)
	stdout, stderr, code := runProcessCommand(
		t, commands, "starport", "models", "search", "gpt-4o", "--json",
	)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
	}
	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Context *int   `json:"context_length"`
			Pricing *struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("decode search output: %v\n%s", err, stdout)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("matches = %d, want 2; output = %s", len(payload.Data), stdout)
	}
	// Matches sort by ID for deterministic output.
	if payload.Data[0].ID != "openai/gpt-4o" || payload.Data[1].ID != "openai/gpt-4o-mini" {
		t.Errorf("match order = %q, %q", payload.Data[0].ID, payload.Data[1].ID)
	}
	mini := payload.Data[1]
	if mini.Context == nil || *mini.Context != 128000 {
		t.Errorf("context = %v, want 128000", mini.Context)
	}
	if mini.Pricing == nil || mini.Pricing.Prompt != "1.5e-07" || mini.Pricing.Completion != "6e-07" {
		t.Errorf("pricing = %+v", mini.Pricing)
	}
}

func TestModelsSearchMatchesEveryTermCaseInsensitively(t *testing.T) {
	commands := staticModelsCommands(t)

	// Both terms must match: "GPT-4O" and "mini" select only the mini model.
	stdout, stderr, code := runProcessCommand(
		t, commands, "starport", "models", "search", "GPT-4O", "mini", "--json",
	)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "openai/gpt-4o-mini") || strings.Contains(stdout, "\"openai/gpt-4o\"") {
		t.Errorf("two-term search output = %s", stdout)
	}

	// An author name matches too.
	stdout, _, code = runProcessCommand(
		t, commands, "starport", "models", "search", "Anthropic", "--json",
	)
	if code != 0 || !strings.Contains(stdout, "anthropic/claude-sonnet-4") {
		t.Errorf("author search code = %d, output = %s", code, stdout)
	}

	// A search that matches nothing still answers an empty list.
	stdout, _, code = runProcessCommand(
		t, commands, "starport", "models", "search", "absent-model", "--json",
	)
	if code != 0 || !strings.Contains(stdout, "\"data\": []") {
		t.Errorf("empty search code = %d, output = %s", code, stdout)
	}
}

func TestModelsSearchNeedsAQuery(t *testing.T) {
	commands := staticModelsCommands(t)
	_, stderr, code := runProcessCommand(t, commands, "starport", "models", "search")
	if code != starportcli.ExitCodeUsage {
		t.Errorf("exit code = %d, want %d", code, starportcli.ExitCodeUsage)
	}
	if !strings.Contains(stderr, "needs a query") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestModelsShowAnswersTheFullProjection(t *testing.T) {
	commands := staticModelsCommands(t)
	stdout, stderr, code := runProcessCommand(
		t, commands, "starport", "models", "show", "openai/gpt-4o-mini", "--json",
	)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr)
	}
	var model view.ModelInfo
	if err := json.Unmarshal([]byte(stdout), &model); err != nil {
		t.Fatalf("decode show output: %v\n%s", err, stdout)
	}
	if model.ID != "openai/gpt-4o-mini" {
		t.Errorf("model ID = %q", model.ID)
	}
	if len(model.Offerings) != 1 || model.Offerings[0].Provider != "openai" {
		t.Errorf("offerings = %+v", model.Offerings)
	}
}

func TestModelsShowRejectsAnUnknownModel(t *testing.T) {
	commands := staticModelsCommands(t)
	_, stderr, code := runProcessCommand(t, commands, "starport", "models", "show", "absent/none")
	if code != starportcli.ExitCodeRuntime {
		t.Errorf("exit code = %d, want %d", code, starportcli.ExitCodeRuntime)
	}
	if !strings.Contains(stderr, "absent/none") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestModelsSearchReadsTheEmbeddedCatalog(t *testing.T) {
	if testing.Short() {
		t.Skip("embedded catalog decode is slow")
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := runContext(
		context.Background(),
		[]string{"starport", "models", "search", "gpt-4o-mini", "--json"},
		bytes.NewReader(nil),
		stdout,
		stderr,
		func(context.Context, starportcli.GatewayOptions) error { return nil },
		noopDevelopmentStarter,
		noopInitializer,
	)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr.String())
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode search output: %v", err)
	}
	if len(payload.Data) == 0 {
		t.Fatal("the embedded catalog answered no gpt-4o-mini match")
	}
}
