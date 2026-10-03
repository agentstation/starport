package console

import "io/fs"

// DocsFS returns the embedded documentation site from dist/docs. It returns
// false when the build has no documentation site.
func DocsFS() (fs.FS, bool) {
	return docsFS(distFiles)
}

func docsFS(dist fs.FS) (fs.FS, bool) {
	docs, err := fs.Sub(dist, "dist/docs")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(docs, "index.html"); err != nil {
		return nil, false
	}
	return docs, true
}
