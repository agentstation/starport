package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/agentstation/starmap"
	urfavecli "github.com/urfave/cli/v3"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/catalog/view"
	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/providers"
	providerauth "github.com/agentstation/starport/internal/providers/auth"
	"github.com/agentstation/starport/internal/providers/connectors"
)

const modelsFormatJSON = "json"

// catalogModels answers the routable model projection the catalog verbs read.
// The process boundary supplies the embedded-catalog loader; tests supply a
// static one.
type catalogModels func(ctx context.Context) ([]view.ModelInfo, error)

// modelSummary is the compact search row. Every field name matches the full
// projection in internal/catalog/view, so search introduces no second model
// vocabulary.
type modelSummary struct {
	ID      string             `json:"id"`
	Name    string             `json:"name,omitempty"`
	Context *int               `json:"context_length,omitempty"`
	Pricing *view.ModelPricing `json:"pricing,omitempty"`
}

// newModelsCommand builds `starport models` with the search and show verbs.
// Both verbs answer offline from the catalog generation embedded in this
// binary, so an agent gets a deterministic answer without a running gateway.
func newModelsCommand(load catalogModels) *urfavecli.Command {
	jsonFlag := func() urfavecli.Flag {
		return &urfavecli.BoolFlag{
			Name:  modelsFormatJSON,
			Usage: "Write machine-readable JSON",
		}
	}
	search := &urfavecli.Command{
		Name: "search", Usage: "Search routable models by ID, name, and author",
		ArgsUsage:    "<query>...",
		OnUsageError: subcommandUsageError,
		Flags:        []urfavecli.Flag{jsonFlag()},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			terms := cmd.Args().Slice()
			if len(terms) == 0 {
				return urfavecli.Exit(
					fmt.Sprintf("%s needs a query", cmd.FullName()),
					starportcli.ExitCodeUsage,
				)
			}
			models, err := load(ctx)
			if err != nil {
				return fmt.Errorf("load the embedded catalog: %w", err)
			}
			matches := searchModels(models, terms)
			return writeModelSummaries(cmd.Writer, matches, cmd.Bool(modelsFormatJSON))
		},
	}
	show := &urfavecli.Command{
		Name: "show", Usage: "Show one model's full catalog projection",
		ArgsUsage:    "<model-id>",
		OnUsageError: subcommandUsageError,
		Flags:        []urfavecli.Flag{jsonFlag()},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if cmd.NArg() != 1 {
				return urfavecli.Exit(
					fmt.Sprintf("%s needs exactly one model ID", cmd.FullName()),
					starportcli.ExitCodeUsage,
				)
			}
			models, err := load(ctx)
			if err != nil {
				return fmt.Errorf("load the embedded catalog: %w", err)
			}
			modelID := cmd.Args().First()
			for _, model := range models {
				if model.ID == modelID {
					return writeModelDetail(cmd.Writer, model, cmd.Bool(modelsFormatJSON))
				}
			}
			return fmt.Errorf("model %q is not in the routable catalog; try \"starport models search\"", modelID)
		},
	}
	return &urfavecli.Command{
		Name: "models", Usage: "Answer catalog questions from the embedded generation",
		OnUsageError: subcommandUsageError,
		Commands:     []*urfavecli.Command{search, show},
		Action: func(_ context.Context, cmd *urfavecli.Command) error {
			_ = urfavecli.ShowSubcommandHelp(cmd)
			if cmd.NArg() > 0 {
				return urfavecli.Exit(
					fmt.Sprintf("unknown models command %q", cmd.Args().First()),
					starportcli.ExitCodeUsage,
				)
			}
			return nil
		},
	}
}

// subcommandUsageError mirrors the command tree's usage handling: show help,
// then exit with the usage code.
func subcommandUsageError(_ context.Context, cmd *urfavecli.Command, err error, _ bool) error {
	_ = urfavecli.ShowSubcommandHelp(cmd)
	return urfavecli.Exit(err, starportcli.ExitCodeUsage)
}

// searchModels keeps every model that contains all query terms in its ID,
// name, or authors, case-insensitively, sorted by ID for stable output.
func searchModels(models []view.ModelInfo, terms []string) []modelSummary {
	matches := make([]modelSummary, 0)
	for _, model := range models {
		haystack := strings.ToLower(modelSearchText(model))
		matched := true
		for _, term := range terms {
			if !strings.Contains(haystack, strings.ToLower(term)) {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, modelSummary{
				ID: model.ID, Name: model.Name,
				Context: model.Context, Pricing: model.Pricing,
			})
		}
	}
	sort.Slice(matches, func(left, right int) bool { return matches[left].ID < matches[right].ID })
	return matches
}

func modelSearchText(model view.ModelInfo) string {
	parts := []string{model.ID, model.Name, model.OwnedBy}
	for _, author := range model.Authors {
		parts = append(parts, author.ID, author.Name)
	}
	return strings.Join(parts, " ")
}

func writeModelSummaries(writer io.Writer, matches []modelSummary, asJSON bool) error {
	if asJSON {
		return writeCatalogJSON(writer, map[string]any{"data": matches})
	}
	for _, match := range matches {
		line := match.ID
		if match.Name != "" {
			line += "  " + match.Name
		}
		if match.Context != nil {
			line += fmt.Sprintf("  context=%d", *match.Context)
		}
		if match.Pricing != nil {
			line += fmt.Sprintf("  prompt=%s completion=%s", match.Pricing.Prompt, match.Pricing.Completion)
		}
		if _, err := fmt.Fprintln(writer, line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(writer, "%d models match\n", len(matches))
	return err
}

func writeModelDetail(writer io.Writer, model view.ModelInfo, asJSON bool) error {
	if asJSON {
		return writeCatalogJSON(writer, model)
	}
	if _, err := fmt.Fprintf(writer, "%s  %s\n", model.ID, model.Name); err != nil {
		return err
	}
	if model.Context != nil {
		if _, err := fmt.Fprintf(writer, "context: %d\n", *model.Context); err != nil {
			return err
		}
	}
	if model.Pricing != nil {
		if _, err := fmt.Fprintf(
			writer,
			"pricing per token: prompt=%s completion=%s %s\n",
			model.Pricing.Prompt, model.Pricing.Completion, model.Pricing.Currency,
		); err != nil {
			return err
		}
	}
	for _, offering := range model.Offerings {
		if _, err := fmt.Fprintf(
			writer,
			"offering: %s/%s operations=%s\n",
			offering.Provider, offering.ProviderModelID, strings.Join(offering.Operations, ","),
		); err != nil {
			return err
		}
	}
	return nil
}

func writeCatalogJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// loadRoutableModels projects the catalog generation embedded in this binary
// through the compiled provider adapters, exactly as the gateway does at
// startup, so the verbs and the gateway answer from one projection.
func loadRoutableModels(ctx context.Context) ([]view.ModelInfo, error) {
	client, err := starmap.NewContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("open the embedded Starmap catalog: %w", err)
	}
	state := client.CurrentCatalogState()
	plane, err := runtimecatalog.Open(catalogStateSource{state: state})
	if err != nil {
		return nil, fmt.Errorf("open the catalog projection: %w", err)
	}
	transports, err := connectors.ProductionTransportRegistry()
	if err != nil {
		return nil, fmt.Errorf("create the provider transport registry: %w", err)
	}
	authentication, err := providerauth.ProductionRegistry()
	if err != nil {
		return nil, fmt.Errorf("create the provider authentication registry: %w", err)
	}
	activations, err := providers.Activate(state.Catalog, transports, authentication, nil)
	if err != nil {
		return nil, fmt.Errorf("activate provider adapters: %w", err)
	}
	if err := plane.ReplaceAdapters(providers.Availability(activations)); err != nil {
		return nil, fmt.Errorf("project provider availability: %w", err)
	}
	return view.Models(plane.Current()), nil
}

type catalogStateSource struct {
	state starmap.CatalogState
}

func (s catalogStateSource) CurrentCatalogState() starmap.CatalogState { return s.state }
