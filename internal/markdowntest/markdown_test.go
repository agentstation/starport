package markdowntest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const page = "Intro\n\n## First\n\nBody\n\n```bash\n## not a heading\n### not a subsection\n```\n\n### Child\n\nChild body\n\n## Second\n\n```text\nOutput\n```\n"

func TestParseKeepsFencedHeadingsInTheBody(t *testing.T) {
	intro, sections := Parse(page)
	if !slices.Equal(intro, []string{"Intro", ""}) {
		t.Fatalf("intro = %q", intro)
	}
	if len(sections) != 2 || sections[0].Heading != "First" || sections[1].Heading != "Second" {
		t.Fatalf("sections = %#v", sections)
	}
	if len(sections[0].Subsections) != 1 || sections[0].Subsections[0].Heading != "Child" {
		t.Fatalf("subsections = %#v", sections[0].Subsections)
	}
	child, found := sections[0].Subsection("Child")
	if !found || child.Text() != "\nChild body\n" {
		t.Fatalf("child = %#v, found = %t", child, found)
	}
	if _, found := sections[0].Subsection("Other"); found {
		t.Fatal("an absent subsection was found")
	}
	fences := Fences(sections[0].Body)
	if len(fences) != 1 || fences[0].Info != "bash" ||
		!slices.Equal(fences[0].Lines, []string{"## not a heading", "### not a subsection"}) {
		t.Fatalf("fences = %#v", fences)
	}
	if fences := Fences(sections[1].Body); len(fences) != 1 || fences[0].Info != "text" || !slices.Equal(fences[0].Lines, []string{"Output"}) {
		t.Fatalf("fences = %#v", fences)
	}
}

func TestReadFileNormalizesLineEndings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.md")
	if err := os.WriteFile(path, []byte("## One\r\n\r\nText\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readNormalized(t, path); got != "## One\n\nText\n" {
		t.Fatalf("readNormalized = %q", got)
	}
	if _, err := os.Stat(filepath.Join(RepositoryRoot(t), "go.mod")); err != nil {
		t.Fatalf("the repository root has no go.mod: %v", err)
	}
	if got := ReadFile(t, "go.mod"); !strings.HasPrefix(got, "module github.com/agentstation/starport\n") {
		t.Fatalf("ReadFile(go.mod) starts with %q", got[:min(len(got), 60)])
	}
}

func TestHeadingSlugAndTableCells(t *testing.T) {
	if got := HeadingSlug("T4 replicated Starport"); got != "t4-replicated-starport" {
		t.Errorf("HeadingSlug = %q", got)
	}
	if got := HeadingSlug("Restricted or air-gapped installation"); got != "restricted-or-air-gapped-installation" {
		t.Errorf("HeadingSlug = %q", got)
	}
	if got := TableCells("| a | `b` |  c |"); !slices.Equal(got, []string{"a", "`b`", "c"}) {
		t.Errorf("TableCells = %q", got)
	}
}
