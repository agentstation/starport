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
	"github.com/agentstation/starport/internal/markdowntest"
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
		headingIndex(headings, "Keep the gateway"),
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
		prefixIndex("Stop the temporary gateway"),
	})
}

func TestReadmeTemporaryPathPrecedesPersistentPath(t *testing.T) {
	document := readReadme(t)
	quickStart := document.section(t, "Quick start")
	persistent := subsection(t, document.section(t, "Keep the gateway"), "Persistent local gateway")

	requireContains(t, fullText(quickStart), "`starport dev`, a temporary gateway", "It keeps no state after it stops.")
	requireLink(t, fullText(quickStart), "docs/site/start/temporary-development.md")
	requireLink(t, persistent.Text(), "docs/site/start/local-persistent.md")
	requireOrdered(t, "temporary and persistent links", []int{
		strings.Index(document.text, "(docs/site/start/temporary-development.md)"),
		strings.Index(document.text, "(docs/site/start/local-persistent.md)"),
	})

	// The refused selector families are the ones that the development page lists.
	terminalOne := subsection(t, quickStart, "Terminal 1:")
	requireContains(t, terminalOne.Text(),
		"It refuses persistent storage selectors before it opens storage.",
		"The refused selectors are the KV, SQL, file, and cache backend settings, and `STARPORT_CATALOG_STATE_DIR`.")
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
		t.Error("the development page does not name the catalog selector that the README names")
	}

	// The README lists the same shutdown losses as the development page.
	var stated []string
	for _, line := range subsection(t, quickStart, "Stop the temporary gateway").Body {
		if item, isItem := strings.CutPrefix(line, "- "); isItem {
			stated = append(stated, item)
		}
	}
	if len(losses) == 0 || !slices.Equal(stated, losses) {
		t.Errorf("README shutdown losses = %q, development page = %q", stated, losses)
	}
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
	keep := document.section(t, "Keep the gateway")
	persistent := subsection(t, keep, "Persistent local gateway")
	deployment := subsection(t, keep, "Team and enterprise deployment")

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
	persistent := subsection(t, document.section(t, "Keep the gateway"), "Persistent local gateway")
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
	for _, fence := range markdowntest.Fences(persistent.Body) {
		if fence.Info == "text" && len(fence.Lines) > 0 {
			samples[fence.Lines[0]] = fence.Lines
		}
	}
	initSample, pathsSample := samples["Initialized Starport."], samples["Configuration directory: "+readmeSampleRoot+"/config"]
	if initSample == nil || pathsSample == nil {
		t.Fatalf("README persistent samples = %q", samples)
	}
	initialized := runReadmeCommand(t, anchored, "init", "--name", "primary-admin")
	requireSampleLabels(t, "starport init", initSample, initialized)
	if !slices.Contains(initialized, "Configuration: "+anchored.ConfigFile) || !slices.Contains(initialized, "Data: "+anchored.DataDir) {
		t.Errorf("starport init output = %q", initialized)
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

func runReadmeCommand(t *testing.T, paths config.Paths, args ...string) []string {
	t.Helper()
	stdout := &bytes.Buffer{}
	err := starportcli.Run(context.Background(), append([]string{"starport"}, args...), starportcli.Dependencies{
		Stdin: bytes.NewReader(nil), Stdout: stdout, Stderr: &bytes.Buffer{},
		RunServer:        func(context.Context, starportcli.GatewayOptions, starportcli.ServerOutput) error { return nil },
		StartDevelopment: noopDevelopmentStarter,
		Initialize: func(context.Context, starportcli.InitOptions) (starportcli.InitResult, error) {
			return starportcli.InitResult{ConfigFile: paths.ConfigFile, DataDir: paths.DataDir, APIKey: "readme-gateway-key"}, nil
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
