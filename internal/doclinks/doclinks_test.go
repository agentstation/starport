package doclinks

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseUsesMarkdownSyntax(t *testing.T) {
	t.Parallel()

	source := []byte("" +
		"[inline](inline.md)\n" +
		"[angle](<target with space.md> \"title\")\n" +
		"[nested](target(v2))\n" +
		"[reference][guide]\n\n" +
		"[guide]: reference.md\n\n" +
		"[![status](local.svg)](https://example.com/status)\n" +
		"[](empty.md)\n" +
		"![](empty.png)\n\n" +
		"`[code](missing-code.md)`\n\n" +
		"```text\n[fenced](missing-fenced.md)\n```\n")

	links, err := Parse(source)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := []Link{
		{Line: 1, Destination: "inline.md"},
		{Line: 2, Destination: "target with space.md"},
		{Line: 3, Destination: "target(v2)"},
		{Line: 4, Destination: "reference.md"},
		{Line: 8, Destination: "https://example.com/status"},
		{Line: 8, Destination: "local.svg"},
		{Line: 9, Destination: "empty.md"},
		{Line: 10, Destination: "empty.png"},
	}
	if !reflect.DeepEqual(links, want) {
		t.Fatalf("Parse() = %#v, want %#v", links, want)
	}
}

func TestCheckFilesValidatesOnlyLocalDestinations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	document := filepath.Join(root, "guide.md")
	if err := os.WriteFile(filepath.Join(root, "existing file.md"), []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "100%.md"), []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := "" +
		"[existing](<existing file.md>?view=1#section)\n" +
		"[percent](100%25.md)\n" +
		"[missing](missing.md)\n" +
		"[anchor](#section)\n" +
		"[remote](https://example.com/missing.md)\n"
	if err := os.WriteFile(document, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	broken, err := CheckFiles(root, []string{document})
	if err != nil {
		t.Fatalf("CheckFiles() error = %v", err)
	}
	want := []BrokenLink{{Source: document, Line: 3, Target: "missing.md"}}
	if !reflect.DeepEqual(broken, want) {
		t.Fatalf("CheckFiles() = %#v, want %#v", broken, want)
	}
}

func TestCheckFilesRejectsPathsOutsideRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := CheckFiles(root, []string{outside}); err == nil {
		t.Fatal("CheckFiles() accepted a source outside its root")
	}
}

func TestCheckFilesResolvesTargetsFromSourceSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	shared := filepath.Join(root, "shared")
	for _, directory := range []string{docs, shared} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sharedGuide := filepath.Join(shared, "guide.md")
	if err := os.WriteFile(sharedGuide, []byte("[target](target.md)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkedGuide := filepath.Join(docs, "guide.md")
	if err := os.Symlink(filepath.Join("..", "shared", "guide.md"), linkedGuide); err != nil {
		t.Skipf("create source symlink: %v", err)
	}
	docsTarget := filepath.Join(docs, "target.md")
	if err := os.WriteFile(docsTarget, []byte("docs target\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	broken, err := CheckFiles(root, []string{linkedGuide})
	if err != nil {
		t.Fatalf("CheckFiles() error = %v", err)
	}
	if len(broken) != 0 {
		t.Fatalf("CheckFiles() = %#v, want no broken links", broken)
	}

	if err := os.Remove(docsTarget); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "target.md"), []byte("shared target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken, err = CheckFiles(root, []string{linkedGuide})
	if err != nil {
		t.Fatalf("CheckFiles() error = %v", err)
	}
	want := []BrokenLink{{Source: linkedGuide, Line: 1, Target: "target.md"}}
	if !reflect.DeepEqual(broken, want) {
		t.Fatalf("CheckFiles() = %#v, want %#v", broken, want)
	}
}
