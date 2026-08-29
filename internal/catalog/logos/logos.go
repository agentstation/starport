// Package logos serves the bundled catalog identity marks.
//
// The SVG set under svg/ is a curated, clean-license bundle (see
// NOTICE.md) keyed by catalog ID, color-first from one icon family so
// marks render consistently. Callers prefer this set and fall back to
// catalog-carried bytes for IDs it does not cover, so the console
// renders identity without any external request.
package logos

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"strings"
)

//go:embed svg
var files embed.FS

// Kind selects the catalog namespace a logo belongs to.
type Kind string

const (
	// KindProviders selects provider marks.
	KindProviders Kind = "providers"
	// KindAuthors selects author marks.
	KindAuthors Kind = "authors"
)

// Bytes returns the bundled SVG for one catalog entity, with its
// content hash for HTTP validators. ok reports whether the bundle
// holds a mark for this kind and ID.
func Bytes(kind Kind, id string) (svg []byte, etag string, ok bool) {
	if (kind != KindProviders && kind != KindAuthors) || !validID(id) {
		return nil, "", false
	}
	data, err := files.ReadFile("svg/" + string(kind) + "/" + id + ".svg")
	if err != nil {
		return nil, "", false
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:16]), true
}

// validID accepts catalog-ID shaped names only, so a request path can
// never select a file outside the bundle.
func validID(id string) bool {
	if id == "" || strings.HasPrefix(id, ".") {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
		default:
			return false
		}
	}
	return !strings.Contains(id, "..")
}
