package console

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestDocsFSReturnsTheDocsSubtree(t *testing.T) {
	docs, ok := docsFS(fstest.MapFS{
		"dist/index.html":                   &fstest.MapFile{Data: []byte("console")},
		"dist/docs/index.html":              &fstest.MapFile{Data: []byte("docs")},
		"dist/docs/operate-starport/a.html": &fstest.MapFile{Data: []byte("page")},
	})
	if !ok {
		t.Fatal("docsFS reported no docs for a tree with dist/docs/index.html")
	}
	if err := fstest.TestFS(docs, "index.html", "operate-starport/a.html"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(docs, "dist"); err == nil {
		t.Fatal("docsFS exposed a path outside dist/docs")
	}
}

func TestDocsFSReportsMissingDocs(t *testing.T) {
	for name, dist := range map[string]fstest.MapFS{
		"no docs directory": {"dist/index.html": &fstest.MapFile{Data: []byte("console")}},
		"no docs index":     {"dist/docs/other.html": &fstest.MapFile{Data: []byte("page")}},
	} {
		if docs, ok := docsFS(dist); ok || docs != nil {
			t.Fatalf("%s: docsFS reported docs", name)
		}
	}
}
