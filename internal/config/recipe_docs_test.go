package config

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var recipeTargets = []string{
	"T1 standalone Starmap",
	"T2 persistent local Starport",
	"T3 one production server",
	"T4 replicated Starport",
	"T5 internal Starmap server",
	"T6 restricted or air-gapped installation",
	"T7 ephemeral development",
}

var recipeSections = []string{
	"Durable owner per role",
	"Memory-serving state",
	"Strict admission operations",
	"Latency targets",
	"Runnable checks",
	"Recovery",
}

// markdownSection is one heading and the body lines up to the next heading of
// the same or a higher level. Fenced code lines stay in the body but never
// start a section.
type markdownSection struct {
	heading     string
	body        []string
	subsections []markdownSection
}

func (s markdownSection) text() string {
	return strings.Join(s.body, "\n")
}

func (s markdownSection) subsection(heading string) (markdownSection, bool) {
	for _, child := range s.subsections {
		if child.heading == heading {
			return child, true
		}
	}
	return markdownSection{}, false
}

// parseRecipePage splits a page into its h2 sections and their h3 sections.
// Text before the first h2 is the intro.
func parseRecipePage(source string) (intro []string, sections []markdownSection) {
	fenced := false
	for _, line := range strings.Split(source, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		switch {
		case !fenced && strings.HasPrefix(line, "## "):
			sections = append(sections, markdownSection{heading: strings.TrimPrefix(line, "## ")})
		case !fenced && strings.HasPrefix(line, "### ") && len(sections) > 0:
			parent := &sections[len(sections)-1]
			parent.subsections = append(parent.subsections, markdownSection{heading: strings.TrimPrefix(line, "### ")})
		case len(sections) == 0:
			intro = append(intro, line)
		default:
			parent := &sections[len(sections)-1]
			if len(parent.subsections) == 0 {
				parent.body = append(parent.body, line)
			} else {
				child := &parent.subsections[len(parent.subsections)-1]
				child.body = append(child.body, line)
			}
		}
	}
	return intro, sections
}

func readRepositoryDoc(t *testing.T, path string) string {
	t.Helper()
	repository, err := filepath.Abs("../..")
	require.NoError(t, err)
	source, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(path)))
	require.NoError(t, err)
	// A Windows checkout converts the docs to CRLF. The tests compare lines.
	return strings.ReplaceAll(string(source), "\r\n", "\n")
}

// headingSlug returns the GitHub-style anchor that rehype-slug assigns.
func headingSlug(heading string) string {
	var slug strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r == ' ':
			slug.WriteRune('-')
		case r == '-' || r == '_' || ('a' <= r && r <= 'z') || ('0' <= r && r <= '9'):
			slug.WriteRune(r)
		}
	}
	return slug.String()
}

func tableCells(line string) []string {
	cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	for index := range cells {
		cells[index] = strings.TrimSpace(cells[index])
	}
	return cells
}

// recipeStatus returns the status line that opens a recipe, or false when the
// first body line is not a status line.
func recipeStatus(section markdownSection) (string, bool) {
	for _, line := range section.body {
		if strings.TrimSpace(line) != "" {
			return strings.CutPrefix(line, "**Status:** ")
		}
	}
	return "", false
}

// TestDeclaredRecipePages proves that each declared target T1 to T7 has one
// recipe with the six required sections, and that each recipe status matches
// the target table.
func TestDeclaredRecipePages(t *testing.T) {
	t.Parallel()

	_, sections := parseRecipePage(readRepositoryDoc(t, "docs/site/architecture/recipes.md"))
	var targets []markdownSection
	for _, section := range sections {
		if section.heading != "Latency profiles" {
			targets = append(targets, section)
		}
	}
	headings := make([]string, 0, len(targets))
	for _, target := range targets {
		headings = append(headings, target.heading)
	}
	require.Equal(t, recipeTargets, headings)

	tableStatus := map[string]string{}
	for _, line := range strings.Split(readRepositoryDoc(t, "docs/site/architecture/targets.md"), "\n") {
		if cells := tableCells(line); strings.HasPrefix(line, "| T") && len(cells) == 4 {
			tableStatus[cells[0]] = cells[3]
		}
	}
	require.Len(t, tableStatus, len(recipeTargets))

	for _, target := range targets {
		id := strings.Fields(target.heading)[0]
		status, found := recipeStatus(target)
		require.True(t, found, "%s has no status line", id)
		require.Equal(t, tableStatus[id], status, "%s status differs from the target table", id)
		require.Equal(t, id == "T2" || id == "T7", status == "Supported", "%s status %q", id, status)

		subheadings := make([]string, 0, len(target.subsections))
		for _, section := range target.subsections {
			subheadings = append(subheadings, section.heading)
			require.NotEmpty(t, strings.TrimSpace(section.text()), "%s %s is empty", id, section.heading)
		}
		require.Equal(t, recipeSections, subheadings, "%s sections", id)
	}

	require.Contains(t, tableStatus["T3"], "Single-process recovery tested")
	require.Contains(t, tableStatus["T4"], "Valkey 7.2.14")
	require.Contains(t, tableStatus["T4"], "PostgreSQL 16.15")
	recovery, _ := targets[3].subsection("Recovery")
	require.Contains(t, recovery.text(), "`master_replid`")
	require.Contains(t, recovery.text(), "A replica promotion can lose each write that only the old primary acknowledged.")
	require.Contains(t, targets[0].text(), "(../operate-starmap/central-server.md)")

	updates := readRepositoryDoc(t, "docs/site/operate-starport/catalog-updates.md")
	require.Contains(t, updates, "## Update policy selector")
	for _, heading := range recipeTargets {
		id := strings.Fields(heading)[0]
		require.Contains(t, updates, "| ["+id+"](../architecture/recipes.md#"+headingSlug(heading)+") |")
	}
}

// TestRecipeLatencyProfiles proves that the recipe latency profiles state the
// engineering targets exactly, keep the UNVERIFIED label, and name CSP22 as the
// qualifier.
func TestRecipeLatencyProfiles(t *testing.T) {
	t.Parallel()

	var targets struct {
		Qualification string `json:"qualification"`
		Runner        struct {
			QualificationOwner string `json:"qualification_owner"`
		} `json:"runner"`
		Recipes map[string]struct {
			Stores          []string           `json:"stores"`
			Replicas        float64            `json:"replicas"`
			MaxStoreRTTP99  *float64           `json:"max_store_rtt_p99_ms"`
			LatencyMillisec map[string]float64 `json:"latency_ms"`
		} `json:"recipes"`
	}
	require.NoError(t, json.Unmarshal([]byte(readRepositoryDoc(t, "docs/performance-targets-v1.json")), &targets))
	require.Equal(t, "UNVERIFIED", targets.Qualification)
	require.Equal(t, "CSP22", targets.Runner.QualificationOwner)
	profiles := []string{"local", "fleet"}
	require.ElementsMatch(t, profiles, slices.Collect(maps.Keys(targets.Recipes)))

	_, sections := parseRecipePage(readRepositoryDoc(t, "docs/site/architecture/recipes.md"))
	require.NotEmpty(t, sections)
	profileSection := sections[0]
	require.Equal(t, "Latency profiles", profileSection.heading)
	require.Contains(t, profileSection.text(), "UNVERIFIED")
	require.Contains(t, profileSection.text(), "CSP22")
	require.Contains(t, profileSection.text(), "`docs/performance-targets-v1.json`")

	rows := map[string][]string{}
	for _, line := range profileSection.body {
		if cells := tableCells(line); strings.HasPrefix(line, "| ") && len(cells) == 3 {
			rows[cells[0]] = cells[1:]
		}
	}
	require.Equal(t, []string{"`local` profile", "`fleet` profile"}, rows["Measure"])

	latencyRows := map[string]string{
		"p50":                   "Paired added latency p50",
		"p95":                   "Paired added latency p95",
		"p99":                   "Paired added latency p99",
		"p999":                  "Paired added latency p99.9",
		"first_byte_added_p99":  "First-byte added latency p99",
		"first_token_added_p99": "First-token added latency p99",
		"stream_forwarding_p99": "Per-event forwarding p99",
	}
	storeNames := map[string]string{
		"badger":      "Badger",
		"sqlite":      "SQLite",
		"filesystem":  "file system",
		"valkey":      "Valkey",
		"postgresql":  "PostgreSQL",
		"objectstore": "object storage",
	}
	number := func(t *testing.T, cell string) float64 {
		t.Helper()
		value, err := strconv.ParseFloat(cell, 64)
		require.NoError(t, err, "table cell %q", cell)
		return value
	}
	for column, name := range profiles {
		recipe := targets.Recipes[name]
		require.Equal(t, recipe.Replicas, number(t, rows["Replicas"][column]), "%s replicas", name)
		if recipe.MaxStoreRTTP99 == nil {
			require.Equal(t, "Not applicable", rows["Maximum store round trip p99"][column], "%s store round trip", name)
		} else {
			require.Equal(t, *recipe.MaxStoreRTTP99, number(t, rows["Maximum store round trip p99"][column]), "%s store round trip", name)
		}
		for _, store := range recipe.Stores {
			require.Contains(t, storeNames, store, "%s store has no documented name", name)
			require.Contains(t, rows["Stores"][column], storeNames[store], "%s stores", name)
		}
		for key, want := range recipe.LatencyMillisec {
			label, documented := latencyRows[key]
			require.True(t, documented, "%s latency key %s has no table row", name, key)
			require.Equal(t, want, number(t, rows[label][column]), "%s %s", name, label)
		}
		require.Len(t, recipe.LatencyMillisec, len(latencyRows), "%s latency keys", name)
	}

	expected := map[string][]string{
		"T1": nil,
		"T2": {"`local` profile"},
		"T3": {"`local` profile"},
		"T4": {"`fleet` profile"},
		"T5": {"`local` profile", "`fleet` profile"},
		"T6": {"`local` profile", "`fleet` profile"},
		"T7": nil,
	}
	profileName := regexp.MustCompile("`(local|fleet)` profile")
	for _, target := range sections[1:] {
		id := strings.Fields(target.heading)[0]
		latency, found := target.subsection("Latency targets")
		require.True(t, found, "%s has no latency section", id)
		text := latency.text()
		require.Contains(t, text, "UNVERIFIED", id)
		require.Contains(t, text, "CSP22", id)
		require.Contains(t, text, "[Latency profiles](#latency-profiles)", id)
		if expected[id] == nil {
			require.NotRegexp(t, "uses the `(local|fleet)` profile", text, id)
			continue
		}
		require.Equal(t, expected[id], profileName.FindAllString(text, -1), id)
		require.Contains(t, text, "This release has no measured value for this recipe.", id)
	}
}
