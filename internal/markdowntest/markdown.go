// Package markdowntest reads repository Markdown for documentation contract
// tests. It splits a page into its h2 and h3 sections, keeps fenced code in
// the section bodies, and normalizes the line endings of a Windows checkout.
package markdowntest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Section is one heading and the body lines up to the next heading of the
// same or a higher level. Fenced code lines stay in the body but never start a
// section.
type Section struct {
	Heading     string
	Body        []string
	Subsections []Section
}

// Text returns the body of the section without its subsections.
func (s Section) Text() string {
	return strings.Join(s.Body, "\n")
}

// Subsection returns the h3 section with the exact heading.
func (s Section) Subsection(heading string) (Section, bool) {
	for _, child := range s.Subsections {
		if child.Heading == heading {
			return child, true
		}
	}
	return Section{}, false
}

// Fence is one fenced code block. Info is the text after the opening fence,
// for example "bash".
type Fence struct {
	Info  string
	Lines []string
}

// Parse splits a page into its h2 sections and their h3 sections. Text before
// the first h2 is the intro.
func Parse(source string) (intro []string, sections []Section) {
	fenced := false
	for _, line := range strings.Split(source, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		switch {
		case !fenced && strings.HasPrefix(line, "## "):
			sections = append(sections, Section{Heading: strings.TrimPrefix(line, "## ")})
		case !fenced && strings.HasPrefix(line, "### ") && len(sections) > 0:
			parent := &sections[len(sections)-1]
			parent.Subsections = append(parent.Subsections, Section{Heading: strings.TrimPrefix(line, "### ")})
		case len(sections) == 0:
			intro = append(intro, line)
		default:
			parent := &sections[len(sections)-1]
			if len(parent.Subsections) == 0 {
				parent.Body = append(parent.Body, line)
			} else {
				child := &parent.Subsections[len(parent.Subsections)-1]
				child.Body = append(child.Body, line)
			}
		}
	}
	return intro, sections
}

// Fences returns the fenced code blocks of the lines in their order.
func Fences(lines []string) []Fence {
	var fences []Fence
	var current *Fence
	for _, line := range lines {
		info, isFence := strings.CutPrefix(line, "```")
		switch {
		case isFence && current == nil:
			fences = append(fences, Fence{Info: strings.TrimSpace(info)})
			current = &fences[len(fences)-1]
		case isFence:
			current = nil
		case current != nil:
			current.Lines = append(current.Lines, line)
		}
	}
	return fences
}

// ReadFile reads a file by its slash-separated path from the repository root.
// The read cannot leave the repository root. A Windows checkout converts the
// docs to CRLF, so the result uses LF only.
func ReadFile(tb testing.TB, path string) string {
	tb.Helper()
	root, err := os.OpenRoot(RepositoryRoot(tb))
	if err != nil {
		tb.Fatalf("open the repository root: %v", err)
	}
	defer func() { _ = root.Close() }()
	source, err := root.ReadFile(filepath.FromSlash(path))
	if err != nil {
		tb.Fatalf("read %s: %v", path, err)
	}
	return normalizeLineEndings(source)
}

func normalizeLineEndings(source []byte) string {
	return strings.ReplaceAll(string(source), "\r\n", "\n")
}

// RepositoryRoot returns the nearest directory above the working directory
// that holds go.mod.
func RepositoryRoot(tb testing.TB) string {
	tb.Helper()
	directory, err := os.Getwd()
	if err != nil {
		tb.Fatalf("find the working directory: %v", err)
	}
	for {
		_, err := os.Stat(filepath.Join(directory, "go.mod"))
		if err == nil {
			return directory
		}
		if !errors.Is(err, fs.ErrNotExist) {
			tb.Fatalf("find the repository root: %v", err)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			tb.Fatal("find the repository root: no go.mod above the working directory")
		}
		directory = parent
	}
}

// HeadingSlug returns the GitHub-style anchor that rehype-slug assigns.
func HeadingSlug(heading string) string {
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

// TableCells splits one Markdown table row into its trimmed cells.
func TableCells(line string) []string {
	cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	for index := range cells {
		cells[index] = strings.TrimSpace(cells[index])
	}
	return cells
}
