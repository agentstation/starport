package inference

import (
	"errors"
	"testing"
)

// TestADocumentNamesOneSourceOrNone states the exclusivity rule at the concept
// that owns it.
//
// Each codec refuses a conflict at its own field path, and the proxy refuses
// one that reached it some other way. Both spell the sources differently, and
// neither can produce every pair: no wire form carries a url beside inline
// bytes. The rule itself lives here, so it is stated once and holds for a
// request built in code as well as one decoded off the wire.
func TestADocumentNamesOneSourceOrNone(t *testing.T) {
	cases := []struct {
		name     string
		document Document
		refused  bool
	}{
		{name: "nothing", document: Document{Filename: "report.pdf"}},
		{name: "a stored reference", document: Document{FileID: "file-1"}},
		{name: "inline bytes", document: Document{Data: []byte("%PDF")}},
		{name: "a url", document: Document{URL: "https://example.test/a.pdf"}},
		{
			name:     "a stored reference beside inline bytes",
			document: Document{FileID: "file-1", Data: []byte("%PDF")},
			refused:  true,
		},
		{
			name:     "a stored reference beside a url",
			document: Document{FileID: "file-1", URL: "https://example.test/a.pdf"},
			refused:  true,
		},
		{
			name:     "inline bytes beside a url",
			document: Document{Data: []byte("%PDF"), URL: "https://example.test/a.pdf"},
			refused:  true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.document.Validate()
			if testCase.refused {
				if !errors.Is(err, ErrDocumentSourceConflict) {
					t.Fatalf("a document naming two sources was accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("a document naming one source was refused: %v", err)
			}
		})
	}
}

// TestAnEmptyFilenameIsNotASource guards the counting itself. Filename and
// Format describe the bytes rather than name them, so a part that carries both
// beside one source is still one source.
func TestAnEmptyFilenameIsNotASource(t *testing.T) {
	document := Document{FileID: "file-1", Filename: "report.pdf", Format: "pdf"}
	if err := document.Validate(); err != nil {
		t.Fatalf("a described stored reference was refused: %v", err)
	}
}
