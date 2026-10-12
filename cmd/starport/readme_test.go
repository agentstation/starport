package main

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/diagnosis"
	"github.com/agentstation/starport/internal/endpoint"
	"github.com/agentstation/starport/internal/markdowntest"
	"github.com/agentstation/starport/internal/setup"
)

// The README tests read the repository README and prove the first-use order,
// the owned facts, and the links to the pages that own the details.

var (
	markdownLink       = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	credentialVariable = regexp.MustCompile(`\b[A-Z][A-Z0-9_]*(?:API_KEY|_TOKEN|MASTER_KEY)\b`)
	apiKeyVariable     = regexp.MustCompile(`\b[A-Z][A-Z0-9_]*API_KEY[A-Z0-9_]*\b`)
	// A latency claim is a number with a time unit. Seconds count only in the
	// performance section, because the recording length is not a latency.
	millisecondClaim = regexp.MustCompile(`(\d+(?:\.\d+)?)[ -]?(ms|milliseconds?|µs|us|microseconds?)\b`)
	secondClaim      = regexp.MustCompile(`(\d+(?:\.\d+)?)[ -]?(s|seconds?)\b`)
	catalogCount     = regexp.MustCompile(`(?i)\b\d[\d,]*\s+(?:[a-z]+\s+)?(?:providers|models)\b`)
)

type readme struct {
	text     string
	sections []markdowntest.Section
}

func readReadme(t *testing.T) readme {
	t.Helper()
	text := markdowntest.ReadFile(t, "README.md")
	_, sections := markdowntest.Parse(text)
	return readme{text: text, sections: sections}
}

func (r readme) section(t *testing.T, heading string) markdowntest.Section {
	t.Helper()
	for _, section := range r.sections {
		if section.Heading == heading {
			return section
		}
	}
	t.Fatalf("README has no %q section", heading)
	return markdowntest.Section{}
}

func (r readme) headings() []string {
	headings := make([]string, 0, len(r.sections))
	for _, section := range r.sections {
		headings = append(headings, section.Heading)
	}
	return headings
}

func subsection(t *testing.T, section markdowntest.Section, prefix string) markdowntest.Section {
	t.Helper()
	for _, child := range section.Subsections {
		if strings.HasPrefix(child.Heading, prefix) {
			return child
		}
	}
	t.Fatalf("README section %q has no subsection %q", section.Heading, prefix)
	return markdowntest.Section{}
}

// fullText returns the section body with each subsection heading and body.
func fullText(section markdowntest.Section) string {
	parts := []string{section.Text()}
	for _, child := range section.Subsections {
		parts = append(parts, "### "+child.Heading, child.Text())
	}
	return strings.Join(parts, "\n")
}

// prose joins wrapped lines so that a claim matches across line breaks.
func prose(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func links(text string) map[string][]string {
	found := map[string][]string{}
	for _, match := range markdownLink.FindAllStringSubmatch(prose(text), -1) {
		found[match[2]] = append(found[match[2]], match[1])
	}
	return found
}

func requireLink(t *testing.T, text, target string) []string {
	t.Helper()
	labels, found := links(text)[target]
	if !found {
		t.Fatalf("missing link to %s", target)
	}
	return labels
}

func requireContains(t *testing.T, text string, claims ...string) {
	t.Helper()
	text = prose(text)
	for _, claim := range claims {
		if !strings.Contains(text, claim) {
			t.Errorf("missing claim %q", claim)
		}
	}
}

func requireOrdered(t *testing.T, label string, positions []int) {
	t.Helper()
	for index, position := range positions {
		if position < 0 {
			t.Fatalf("%s: item %d is missing", label, index)
		}
		if index > 0 && position <= positions[index-1] {
			t.Fatalf("%s: item %d at %d is not after item %d at %d", label, index, position, index-1, positions[index-1])
		}
	}
}

func headingIndex(headings []string, heading string) int {
	return slices.Index(headings, heading)
}

// requireAnchor proves that a fragment link names a heading of its page.
func requireAnchor(t *testing.T, target string) {
	t.Helper()
	path, fragment, found := strings.Cut(target, "#")
	if !found {
		t.Fatalf("link %s has no fragment", target)
	}
	for _, line := range strings.Split(markdowntest.ReadFile(t, path), "\n") {
		if heading, isHeading := strings.CutPrefix(strings.TrimLeft(line, "#"), " "); isHeading &&
			strings.HasPrefix(line, "#") && markdowntest.HeadingSlug(heading) == fragment {
			return
		}
	}
	t.Fatalf("link %s names no heading of %s", target, path)
}

func TestReadmeFollowsTheFirstUseSequence(t *testing.T) {
	document := readReadme(t)
	headings := document.headings()
	requireOrdered(t, "README sections", []int{
		headingIndex(headings, "Install"),
		headingIndex(headings, "Quick start"),
		headingIndex(headings, "Connect a client"),
		headingIndex(headings, "Serve and deploy"),
		headingIndex(headings, "Configuration"),
		headingIndex(headings, "Performance"),
	})

	quickStart := document.section(t, "Quick start")
	children := make([]string, 0, len(quickStart.Subsections))
	for _, child := range quickStart.Subsections {
		children = append(children, child.Heading)
	}
	prefixIndex := func(prefix string) int {
		return slices.IndexFunc(children, func(heading string) bool { return strings.HasPrefix(heading, prefix) })
	}
	requireOrdered(t, "quick start subsections", []int{
		prefixIndex("Inspect the catalog"),
		prefixIndex("Credential roles"),
		prefixIndex("Terminal 1:"),
		prefixIndex("Terminal 2:"),
		prefixIndex("Stop and restart the gateway"),
		prefixIndex("Run a scratch gateway"),
	})
}

// TestReadmePersistentDevIsTheFirstPath proves that the quick start runs the
// persistent development gateway, restarts it with the same key, and leaves
// the scratch gateway and the team bootstrap for later sections.
func TestReadmePersistentDevIsTheFirstPath(t *testing.T) {
	document := readReadme(t)
	quickStart := document.section(t, "Quick start")
	requireContains(t, quickStart.Text(), "The quick start runs `starport dev`, a local gateway that keeps its keys and data between runs.")

	// Terminal 1 starts the persistent gateway, and only the scratch section
	// adds --ephemeral.
	terminalOne := subsection(t, quickStart, "Terminal 1:")
	if commands := readmeStarportCommands(terminalOne); !slices.Equal(commands, []string{"starport dev"}) {
		t.Errorf("Terminal 1 commands = %q, want the persistent starport dev", commands)
	}
	requireContains(t, terminalOne.Text(),
		"The first run prints the gateway API key once.",
		"The first identity has the name `"+setup.DefaultAPIKeyName+"`.",
		"Keep this terminal open.")

	// The fallback order and the fallback line are the ones that dev uses.
	ports := endpoint.DeveloperPorts(7827, func(int) int { return 0 })
	requireContains(t, terminalOne.Text(),
		"When another process holds port "+strconv.Itoa(ports[0])+", `dev` tries other ports in this order:",
		"1. Port "+strconv.Itoa(ports[1])+".", "2. Port "+strconv.Itoa(ports[2])+".",
		"3. A random free port from 7000 through 7999.", "4. Any free port.",
		"for example `Port 7827 is busy. Bound port 7373.`")
	fallback := runReadmeDevelopment(t, config.Paths{}, starportcli.DevelopmentSession{BusyPort: 7827, URL: "http://127.0.0.1:7373"}, false)
	if !slices.Contains(fallback, "Port 7827 is busy. Bound port 7373.") {
		t.Errorf("starport dev fallback output = %q", fallback)
	}

	// The restart prints the banner without a key.
	restart := subsection(t, quickStart, "Stop and restart the gateway")
	if commands := readmeStarportCommands(restart); !slices.Equal(commands, []string{"starport dev"}) {
		t.Errorf("restart commands = %q, want the persistent starport dev", commands)
	}
	requireContains(t, restart.Text(),
		"It prints no gateway API key, and the key from the first run still works.",
		"The error names `"+setup.RecoverCommand+"`, which retries the interrupted setup.",
		"`starport serve` uses the same root, configuration, and keys.")
	requireLink(t, restart.Text(), "docs/site/start/local-persistent.md")
	var restartSample []string
	for _, fence := range markdowntest.Fences(restart.Body) {
		if fence.Info == "text" {
			restartSample = fence.Lines
		}
	}
	second := runReadmeDevelopment(t, config.Paths{}, readmeDevelopmentSession(), false)
	if len(restartSample) != len(second) {
		t.Fatalf("restart sample = %q, starport dev prints %q", restartSample, second)
	}
	requireSampleLabels(t, "starport dev (second run)", restartSample, second)

	// The scratch gateway is the opt-in path, and it keeps nothing.
	scratch := subsection(t, quickStart, "Run a scratch gateway")
	if commands := readmeStarportCommands(scratch); !slices.Equal(commands, []string{"starport dev --ephemeral"}) {
		t.Errorf("scratch commands = %q, want starport dev --ephemeral", commands)
	}
	requireContains(t, scratch.Text(),
		"A scratch gateway uses in-memory state for Badger and SQLite.", "It creates no configuration files.",
		"It prints a new gateway API key on each run.",
		"refuses persistent storage selectors before it opens storage.",
		"It has no upgrade path to persistent state.")
	requireLink(t, scratch.Text(), "docs/site/start/temporary-development.md")
	requireOrdered(t, "persistent and scratch commands", []int{
		strings.Index(document.text, "\nstarport dev\n"),
		strings.Index(document.text, "\nstarport dev --ephemeral\n"),
	})

	// The scratch page lists the refused selector families and the losses.
	_, development := markdowntest.Parse(markdowntest.ReadFile(t, "docs/site/start/temporary-development.md"))
	var families []string
	var losses []string
	for _, section := range development {
		for _, line := range section.Body {
			item, isItem := strings.CutPrefix(line, "- ")
			if !isItem {
				continue
			}
			switch section.Heading {
			case "Settings that development mode refuses":
				family, _, _ := strings.Cut(item, ":")
				families = append(families, family)
			case "What the gateway loses at shutdown":
				losses = append(losses, item)
			}
		}
	}
	if want := []string{"KV selectors", "SQL selectors", "File selectors", "Cache selectors", "Catalog selector"}; !slices.Equal(families, want) {
		t.Errorf("development page selector families = %q, want %q", families, want)
	}
	if !strings.Contains(markdowntest.ReadFile(t, "docs/site/start/temporary-development.md"), "- Catalog selector: `STARPORT_CATALOG_STATE_DIR`.") {
		t.Error("the development page does not name the catalog selector")
	}
	if len(losses) == 0 {
		t.Error("the development page lists no shutdown losses")
	}

	// Configured storage starts with auth bootstrap in the team section, not in
	// the quick start.
	if strings.Contains(fullText(quickStart), "auth bootstrap --name") {
		t.Error("the quick start bootstraps configured storage")
	}
	deployment := subsection(t, document.section(t, "Serve and deploy"), "Team and enterprise deployment")
	requireContains(t, deployment.Text(),
		"Configured storage does not initialize itself.",
		"`starport auth bootstrap --name <name>` creates the first named identity.",
		"docker compose run --rm starport auth bootstrap --name primary-admin")
}

// readmeStarportCommands returns the starport lines of the bash blocks of a
// section.
func readmeStarportCommands(section markdowntest.Section) []string {
	var commands []string
	for _, fence := range markdowntest.Fences(section.Body) {
		if fence.Info != "bash" {
			continue
		}
		for _, line := range fence.Lines {
			if strings.HasPrefix(line, "starport ") {
				commands = append(commands, line)
			}
		}
	}
	return commands
}

func TestReadmeInspectsTheCatalogBeforeAnyCredential(t *testing.T) {
	document := readReadme(t)
	inspection := subsection(t, document.section(t, "Quick start"), "Inspect the catalog")
	commands := readmeCatalogCommands(t, inspection)
	if len(commands) != 2 || commands[0][2] != "search" || commands[1][2] != "show" {
		t.Fatalf("catalog commands = %q", commands)
	}
	firstCredential := credentialVariable.FindStringIndex(document.text)
	if firstCredential == nil {
		t.Fatal("README names no credential variable")
	}
	for _, command := range commands {
		line := strings.Join(command, " ")
		position := strings.Index(document.text, line)
		if position < 0 || position > firstCredential[0] {
			t.Errorf("%q at %d does not precede the first credential variable at %d", line, position, firstCredential[0])
		}
	}
	requireContains(t, inspection.Text(), "These commands need no credential or network access.")
}

// readmeCatalogCommands returns the starport commands of the catalog
// inspection block as argument lists.
func readmeCatalogCommands(t *testing.T, inspection markdowntest.Section) [][]string {
	t.Helper()
	var commands [][]string
	for _, fence := range markdowntest.Fences(inspection.Body) {
		if fence.Info != "bash" {
			continue
		}
		for _, line := range fence.Lines {
			if fields := strings.Fields(line); len(fields) > 2 && fields[0] == "starport" && fields[1] == "models" {
				commands = append(commands, fields)
			}
		}
	}
	return commands
}

func TestReadmeNamesTheThreeCredentialRolesInOrder(t *testing.T) {
	document := readReadme(t)
	roles := prose(subsection(t, document.section(t, "Quick start"), "Credential roles").Text())
	names := []string{"**gateway API key**", "**provider inference credential**", "**catalog-acquisition credential**"}
	variables := []string{"`STARPORT_API_KEY`", "`OPENAI_API_KEY`", "`STARPORT_CATALOG_SOURCE_API_KEY`"}
	positions := make([]int, 0, len(names))
	for _, name := range names {
		positions = append(positions, strings.Index(roles, name))
	}
	requireOrdered(t, "credential roles", positions)
	for index, variable := range variables {
		end := len(roles)
		if index+1 < len(positions) {
			end = positions[index+1]
		}
		if !strings.Contains(roles[positions[index]:end], variable) {
			t.Errorf("role %s does not name %s", names[index], variable)
		}
	}
	requireLink(t, roles, "docs/site/start/keys-and-roles.md")
}

// TestReadmeKeepsCredentialRolesApart proves that no example block mixes the
// gateway key, a provider credential, and the catalog-acquisition credential.
func TestReadmeKeepsCredentialRolesApart(t *testing.T) {
	document := readReadme(t)
	examples := 0
	for _, fence := range markdowntest.Fences(strings.Split(document.text, "\n")) {
		roles := map[string]bool{}
		for _, variable := range apiKeyVariable.FindAllString(strings.Join(fence.Lines, "\n"), -1) {
			roles[credentialRole(variable)] = true
		}
		if len(roles) > 0 {
			examples++
		}
		if len(roles) > 1 {
			t.Errorf("one %s example mixes credential roles %q:\n%s", fence.Info, slices.Sorted(maps.Keys(roles)), strings.Join(fence.Lines, "\n"))
		}
	}
	if examples < 3 {
		t.Errorf("README has %d credential examples, want the gateway, provider, and acquisition examples", examples)
	}
}

func credentialRole(variable string) string {
	switch variable {
	case "STARPORT_API_KEY":
		return "gateway"
	case "STARPORT_CATALOG_SOURCE_API_KEY":
		return "catalog acquisition"
	default:
		return "provider inference"
	}
}

func TestReadmeLinksTheStorageAndRecipePages(t *testing.T) {
	document := readReadme(t)
	persistent := subsection(t, document.section(t, "Quick start"), "Where the gateway keeps its data")
	deployment := subsection(t, document.section(t, "Serve and deploy"), "Team and enterprise deployment")

	labels := requireLink(t, deployment.Text(), "docs/site/architecture/recipes.md#t4-replicated-starport")
	if len(labels) != 1 {
		t.Fatalf("fleet recipe link labels = %q", labels)
	}
	for _, part := range []string{"Valkey", "PostgreSQL", "shared blob bytes", "private replica state"} {
		if !strings.Contains(labels[0], part) {
			t.Errorf("fleet recipe link text %q does not name %s", labels[0], part)
		}
	}
	requireContains(t, deployment.Text(), "That recipe needs all four parts together.")
	requireAnchor(t, "docs/site/architecture/recipes.md#t4-replicated-starport")
	requireLink(t, deployment.Text(), "docs/site/architecture/targets.md#target-table")
	requireAnchor(t, "docs/site/architecture/targets.md#target-table")

	requireLink(t, persistent.Text(), "docs/site/configure/paths.md")
	requireLink(t, persistent.Text(), "docs/site/storage/backends.md")
	requireLink(t, persistent.Text(), "docs/site/storage/caches.md")
	requireContains(t, persistent.Text(),
		"Badger holds the durable KV records",
		"Ristretto holds disposable memory caches.",
		"A cache never holds durable state.")
	requireContains(t, markdowntest.ReadFile(t, "docs/site/storage/caches.md"), "A cache never holds durable state")
}

// TestReadmeStatesThePathAnchors proves the path claims in the README and
// binds them, and the sample output, to the loader and the CLI.
func TestReadmeStatesThePathAnchors(t *testing.T) {
	document := readReadme(t)
	quickStart := document.section(t, "Quick start")
	persistent := subsection(t, quickStart, "Where the gateway keeps its data")
	requireContains(t, persistent.Text(),
		"`STARPORT_HOME` is the shared path anchor.",
		"It puts the configuration, data, state, and cache roots under one directory.",
		"`STARPORT_CONFIG_DIR` moves only the configuration root.")
	for _, sentence := range strings.SplitAfter(prose(document.text), ". ") {
		if strings.Contains(sentence, "STARPORT_CONFIG_DIR") &&
			(!strings.Contains(sentence, "only") || strings.Contains(sentence, "data")) {
			t.Errorf("README claims more for STARPORT_CONFIG_DIR than the configuration root: %q", sentence)
		}
	}

	home := t.TempDir()
	moved := filepath.Join(t.TempDir(), "configuration")
	anchored := loadPaths(t, map[string]string{"STARPORT_HOME": home})
	for _, root := range []struct{ got, want string }{
		{anchored.ConfigDir, filepath.Join(home, "config")}, {anchored.DataDir, filepath.Join(home, "data")},
		{anchored.StateDir, filepath.Join(home, "state")}, {anchored.CacheDir, filepath.Join(home, "cache")},
	} {
		if root.got != root.want {
			t.Errorf("STARPORT_HOME root = %s, want %s", root.got, root.want)
		}
	}
	configured := loadPaths(t, map[string]string{"STARPORT_HOME": home, "STARPORT_CONFIG_DIR": moved})
	if configured.ConfigDir != moved || configured.DataDir != anchored.DataDir ||
		configured.StateDir != anchored.StateDir || configured.CacheDir != anchored.CacheDir {
		t.Errorf("STARPORT_CONFIG_DIR moved more than the configuration root: %#v", configured)
	}

	// Each sample line in the README carries a label of the real output, in order.
	samples := map[string][]string{}
	for _, section := range []markdowntest.Section{subsection(t, quickStart, "Terminal 1:"), persistent} {
		for _, fence := range markdowntest.Fences(section.Body) {
			if fence.Info == "text" && len(fence.Lines) > 0 {
				samples[fence.Lines[0]] = fence.Lines
			}
		}
	}
	initSample, pathsSample := samples["Initialized Starport."], samples["Configuration directory: "+readmeSampleRoot+"/config"]
	if initSample == nil || pathsSample == nil {
		t.Fatalf("README persistent samples = %q", samples)
	}
	// The first run of dev prints the initialization block and then the banner.
	initialized := runReadmeDevelopment(t, anchored, readmeDevelopmentSession(), true)
	if len(initSample) != len(initialized) {
		t.Fatalf("first-run sample = %q, starport dev prints %q", initSample, initialized)
	}
	requireSampleLabels(t, "starport dev (first run)", initSample, initialized)
	if !slices.Contains(initialized, "Configuration: "+anchored.ConfigFile) || !slices.Contains(initialized, "Data: "+anchored.DataDir) {
		t.Errorf("starport dev output = %q", initialized)
	}
	requireSampleLabels(t, "starport config paths", pathsSample, runReadmeCommand(t, anchored, "config", "paths"))
}

const readmeSampleRoot = "/Users/<user>/Library/Application Support/starport"

func loadPaths(t *testing.T, environment map[string]string) config.Paths {
	t.Helper()
	loaded, err := config.NewLoader().WithEnvironment(environment).WithEnvFiles().Load(t.Context())
	if err != nil {
		t.Fatalf("load configuration with %v: %v", environment, err)
	}
	return loaded.EffectivePaths()
}

// readmeDevelopmentSession is a persistent session on the standard port that
// binds nothing.
func readmeDevelopmentSession() starportcli.DevelopmentSession {
	return starportcli.DevelopmentSession{URL: "http://127.0.0.1:7827"}
}

// runReadmeDevelopment runs starport dev --no-open against a session that
// serves nothing. A first run delivers the key that a clean root mints.
func runReadmeDevelopment(t *testing.T, paths config.Paths, session starportcli.DevelopmentSession, firstRun bool) []string {
	t.Helper()
	session.ConsoleURL = session.URL + "/launch?lt=readme-ticket"
	session.Run = func(context.Context) error { return nil }
	session.Close = func(context.Context) error { return nil }
	start := func(ctx context.Context, _ starportcli.DevelopmentOptions, deliver starportcli.CredentialDelivery) (starportcli.DevelopmentSession, error) {
		if firstRun {
			if err := deliver(ctx, starportcli.InitResult{
				APIKeyName: setup.DefaultAPIKeyName, ConfigFile: paths.ConfigFile, DataDir: paths.DataDir, APIKey: "readme-gateway-key",
			}); err != nil {
				return starportcli.DevelopmentSession{}, err
			}
		}
		return session, nil
	}
	return runReadmeCommandWith(t, paths, start, "dev", "--no-open")
}

func runReadmeCommand(t *testing.T, paths config.Paths, args ...string) []string {
	t.Helper()
	return runReadmeCommandWith(t, paths, noopDevelopmentStarter, args...)
}

func runReadmeCommandWith(t *testing.T, paths config.Paths, startDevelopment starportcli.DevelopmentStarter, args ...string) []string {
	t.Helper()
	stdout := &bytes.Buffer{}
	err := starportcli.Run(context.Background(), append([]string{"starport"}, args...), starportcli.Dependencies{
		Stdin: bytes.NewReader(nil), Stdout: stdout, Stderr: &bytes.Buffer{},
		// serve prints what a first run prints and starts no gateway.
		RunServer: func(ctx context.Context, _ starportcli.GatewayOptions, output starportcli.ServerOutput) error {
			return output.DeliverCredential(ctx, starportcli.InitResult{
				APIKeyName: setup.DefaultAPIKeyName, ConfigFile: paths.ConfigFile, DataDir: paths.DataDir, APIKey: "readme-gateway-key",
			})
		},
		StartDevelopment: startDevelopment,
		Initialize: func(context.Context, starportcli.InitOptions) (starportcli.InitResult, error) {
			return starportcli.InitResult{}, nil
		},
		LoadConfig: func(ctx context.Context) (*config.Config, error) {
			return config.NewLoader().WithPaths(paths).WithEnvironment(nil).WithEnvFiles().Load(ctx)
		},
		ResolvePaths: func() (config.Paths, error) { return paths, nil },
		Diagnose: func(context.Context, diagnosis.Options) diagnosis.Report {
			return diagnosis.Report{}
		},
	})
	if err != nil {
		t.Fatalf("starport %s: %v", strings.Join(args, " "), err)
	}
	return strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
}

func requireSampleLabels(t *testing.T, command string, sample, output []string) {
	t.Helper()
	label := func(line string) string {
		name, _, found := strings.Cut(line, ": ")
		if !found {
			return line
		}
		return name
	}
	if len(sample) > len(output) {
		t.Fatalf("%s sample has %d lines, the command prints %d", command, len(sample), len(output))
	}
	for index, line := range sample {
		if label(line) != label(output[index]) {
			t.Errorf("%s sample line %d = %q, the command prints %q", command, index+1, line, output[index])
		}
	}
}

func TestReadmePerformanceClaimsAreQualified(t *testing.T) {
	document := readReadme(t)
	performance := document.section(t, "Performance")
	requireLink(t, performance.Text(), "docs/performance-targets-v1.json")
	requireLink(t, performance.Text(), "docs/PERFORMANCE.md")
	requireContains(t, performance.Text(), "The profile marks these targets UNVERIFIED.", "CSP22 owns their qualification")

	targets := readPerformanceTargets(t)
	if targets.Qualification != "UNVERIFIED" || targets.Runner.QualificationOwner != "CSP22" {
		t.Fatalf("performance profile qualification = %q by %q", targets.Qualification, targets.Runner.QualificationOwner)
	}
	var allowed []float64
	for _, recipe := range targets.Recipes {
		for _, value := range recipe.LatencyMilliseconds {
			allowed = append(allowed, value)
		}
	}
	if len(allowed) == 0 {
		t.Fatal("the performance profile has no latency targets")
	}
	for _, claim := range readmeLatencyClaims(document.text, performance.Text()) {
		if !slices.Contains(allowed, claim) {
			t.Errorf("README latency claim %g ms is not a value of docs/performance-targets-v1.json", claim)
		}
	}
}

type performanceTargets struct {
	Qualification string `json:"qualification"`
	Runner        struct {
		QualificationOwner string `json:"qualification_owner"`
	} `json:"runner"`
	Recipes map[string]struct {
		LatencyMilliseconds map[string]float64 `json:"latency_ms"`
	} `json:"recipes"`
}

func readPerformanceTargets(t *testing.T) performanceTargets {
	t.Helper()
	var targets performanceTargets
	if err := json.Unmarshal([]byte(markdowntest.ReadFile(t, "docs/performance-targets-v1.json")), &targets); err != nil {
		t.Fatalf("decode the performance profile: %v", err)
	}
	return targets
}

// readmeLatencyClaims returns each latency number in milliseconds.
func readmeLatencyClaims(text, performance string) []float64 {
	scale := map[string]float64{"ms": 1, "millisecond": 1, "milliseconds": 1, "µs": 0.001, "us": 0.001, "microsecond": 0.001, "microseconds": 0.001, "s": 1000, "second": 1000, "seconds": 1000}
	var claims []float64
	collect := func(pattern *regexp.Regexp, source string) {
		for _, match := range pattern.FindAllStringSubmatch(prose(source), -1) {
			value, err := strconv.ParseFloat(match[1], 64)
			if err != nil {
				panic(err)
			}
			claims = append(claims, value*scale[match[2]])
		}
	}
	collect(millisecondClaim, text)
	collect(secondClaim, performance)
	return claims
}

func TestReadmeLatencyClaimsReadEachUnit(t *testing.T) {
	got := readmeLatencyClaims("p50 of 0.5 ms, p99 of 2 milliseconds, and 250 µs per event.", "p99.9 below 0.008 seconds")
	if want := []float64{0.5, 2, 0.25, 8}; !slices.Equal(got, want) {
		t.Errorf("latency claims = %v, want %v", got, want)
	}
	if got := readmeLatencyClaims("Watch the 38-second first request.", ""); len(got) != 0 {
		t.Errorf("a recording length outside the performance section is a latency claim: %v", got)
	}
}

func TestReadmeOmitsCatalogCounts(t *testing.T) {
	if !catalogCount.MatchString("The generation lists 17 providers and routes 511 models.") {
		t.Fatal("the catalog count pattern misses a count")
	}
	if counts := catalogCount.FindAllString(prose(readReadme(t).text), -1); len(counts) > 0 {
		t.Errorf("README states catalog counts that no test binds: %q", counts)
	}
}
