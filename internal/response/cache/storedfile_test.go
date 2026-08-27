package cache

import (
	"testing"

	"github.com/agentstation/starport/internal/inference"
)

// storedDocumentPart names a document this gateway holds rather than carrying
// its bytes.
func storedDocumentPart(fileID string) inference.ContentPart {
	return inference.ContentPart{
		Kind:     inference.ContentDocument,
		Document: &inference.Document{FileID: fileID, Filename: "report.pdf"},
	}
}

// TestTwoStoredFilesKeyApart holds FIL-V19.
//
// A stored file reference is the whole of what the request says about those
// bytes. If the identifier missed the key, two requests asking about two
// different documents would share one entry, and the second caller would read
// an answer about a file it never sent.
func TestTwoStoredFilesKeyApart(t *testing.T) {
	first, err := ChatKey(mediaIdentity(storedDocumentPart("file-1")))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ChatKey(mediaIdentity(storedDocumentPart("file-2")))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two stored file identifiers produced one cache key")
	}

	// And the same identifier keys the same, or a stored file would never
	// produce a hit at all.
	repeat, err := ChatKey(mediaIdentity(storedDocumentPart("file-1")))
	if err != nil {
		t.Fatal(err)
	}
	if repeat != first {
		t.Fatalf("the same stored file keyed twice: %q and %q", first, repeat)
	}
}

// TestAStoredFileStaysCacheEligible states the rule that separates a stored
// reference from a remote one.
//
// A URL is a promise: the bytes behind it can change while the request stays
// word for word the same, so the gateway refuses to cache it. A stored file is
// not a promise. The gateway wrote those bytes once, never rewrites them, and
// never reuses the identifier, so the reference names one fixed payload for as
// long as it resolves at all.
func TestAStoredFileStaysCacheEligible(t *testing.T) {
	if _, err := ChatKey(mediaIdentity(storedDocumentPart("file-1"))); err != nil {
		t.Fatalf("a stored file reference was refused: %v", err)
	}

	// The remote arm of the same field is still refused, so this eligibility
	// belongs to the identifier rather than to the document kind.
	remote, ok := remoteMediaPart(inference.ContentDocument)
	if !ok {
		t.Fatal("the document kind has no remote form")
	}
	if _, err := ChatKey(mediaIdentity(remote)); err == nil {
		t.Fatal("a remote document was accepted")
	}
}

// TestAStoredFileAndItsBytesKeyApart states that resolution does not collapse
// the two spellings into one entry.
//
// The gateway resolves a stored reference into the same bytes an inline caller
// sends, so the two requests reach the provider identically. They key
// differently all the same, because the cache middleware runs ahead of that
// resolution and sees the reference. That costs one duplicate entry and buys a
// lookup that never reads a file.
func TestAStoredFileAndItsBytesKeyApart(t *testing.T) {
	stored, err := ChatKey(mediaIdentity(storedDocumentPart("file-1")))
	if err != nil {
		t.Fatal(err)
	}
	inline, err := ChatKey(mediaIdentity(inference.ContentPart{
		Kind:     inference.ContentDocument,
		Document: &inference.Document{URL: "data:application/pdf;base64,cGF5bG9hZA==", Filename: "report.pdf"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if stored == inline {
		t.Fatal("a stored reference and inline bytes shared one cache key")
	}
}
