package document_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/document"
)

// PLG-V04 and PLG-V05. These tests hold the native engine's contract: it reads
// a text layer that is really there, it says so when there is none, and it
// refuses rather than crashes on input a caller controls.
//
// The fixtures under testdata are real PDFs from three writers, because a
// single writer proves only that this engine reads that writer. A PDF stores
// no spaces and no reading order, so what a writer emits decides what an
// extractor has to reconstruct, and the three here disagree about all of it.

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return data
}

func extract(t *testing.T, input document.Input) (document.Extraction, error) {
	t.Helper()
	return document.NewExtractor(document.Limits{}).Extract(t.Context(), input)
}

// TestSeveralWritersProduceTextThisEngineReads holds the first half of
// PLG-V04. Each fixture carries the same sentence, so a writer whose output
// this engine reads badly shows up as a missing phrase rather than as a
// difference from some recorded blob.
func TestSeveralWritersProduceTextThisEngineReads(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		writer  string
		phrases []string
	}{
		{
			name:   "quartz",
			file:   "quartz.pdf",
			writer: "macOS Quartz PDFContext, which writes one run per line",
			phrases: []string{
				"Starport native extraction proof.",
				"The quick brown fox jumps over the lazy dog.",
			},
		},
		{
			name:   "pdftex",
			file:   "pdftex.pdf",
			writer: "pdfTeX, which positions each word and writes no spaces",
			phrases: []string{
				"Starport native extraction proof.",
				"The quick brown fox jumps over the lazy dog.",
			},
		},
		{
			name:   "handwritten",
			file:   "handwritten.pdf",
			writer: "a minimal uncompressed writer with one Tj operator",
			phrases: []string{
				"Starport native extraction proof.",
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			extraction, err := extract(t, document.Input{
				Data:     fixture(t, testCase.file),
				Format:   "pdf",
				Filename: testCase.file,
			})
			require.NoError(t, err)
			require.Equal(t, 1, extraction.PageCount())
			require.False(t, extraction.Scanned,
				"a document with a text layer is not a scanned document")
			require.False(t, extraction.Pages[0].Scanned)
			for _, phrase := range testCase.phrases {
				require.Containsf(t, extraction.Text, phrase,
					"text from %s lost %q", testCase.writer, phrase)
			}
		})
	}
}

// TestAWordGapBecomesASpace is the reason assemble measures gaps rather than
// concatenating runs. pdfTeX writes each word at its own coordinate and emits
// no space character at all. An extractor that ignores the gaps returns
// "Thequickbrownfox", and a model handed that reads a different document than
// the caller attached.
func TestAWordGapBecomesASpace(t *testing.T) {
	extraction, err := extract(t, document.Input{
		Data:   fixture(t, "pdftex.pdf"),
		Format: "pdf",
	})
	require.NoError(t, err)
	require.Contains(t, extraction.Text, "The quick brown fox")
	require.NotContains(t, extraction.Text, "Thequickbrownfox")
}

// TestAScannedDocumentReportsScannedRatherThanEmptyText holds the second half
// of PLG-V04. The fixture is one page holding an image and no text operator,
// which is what a scan is. Returning an error here would be wrong: the read
// succeeded, and what it found is the answer that sends the page to the
// recognition engine.
func TestAScannedDocumentReportsScannedRatherThanEmptyText(t *testing.T) {
	extraction, err := extract(t, document.Input{
		Data:   fixture(t, "scanned.pdf"),
		Format: "application/pdf",
	})
	require.NoError(t, err)
	require.True(t, extraction.Scanned)
	require.Equal(t, 1, extraction.PageCount())
	require.True(t, extraction.Pages[0].Scanned)
	require.Empty(t, extraction.Text)
}

// TestEveryPageIsReportedInOrder holds the page count half of PLG-V04. A
// caller that gets the right total and the wrong order gets a document that
// reads as nonsense, so the order is asserted per page rather than in total.
func TestEveryPageIsReportedInOrder(t *testing.T) {
	extraction, err := extract(t, document.Input{
		Data:   fixture(t, "many-pages.pdf"),
		Format: "pdf",
	})
	require.NoError(t, err)
	require.Equal(t, 12, extraction.PageCount())
	require.False(t, extraction.Scanned)
	for index, page := range extraction.Pages {
		require.Equal(t, index+1, page.Number)
		require.Contains(t, page.Text, "Page "+strconv.Itoa(index+1)+" of the page budget fixture")
	}
	require.Equal(t, 11, strings.Count(extraction.Text, "\n\n"),
		"pages are separated so a model can tell where one ended")
}

// TestThePageBudgetRefusesBeforeAnyPageIsRead holds the page bound. The
// refusal has to arrive before the reader walks the pages, because the whole
// point of the bound is that an oversized upload costs almost nothing.
func TestThePageBudgetRefusesBeforeAnyPageIsRead(t *testing.T) {
	extractor := document.NewExtractor(document.Limits{MaxPages: 5})
	extraction, err := extractor.Extract(t.Context(), document.Input{
		Data:   fixture(t, "many-pages.pdf"),
		Format: "pdf",
	})
	require.ErrorIs(t, err, document.ErrPageBudgetExceeded)
	require.Contains(t, err.Error(), "12 pages")
	require.Empty(t, extraction.Pages,
		"a refused document returns no partial extraction")
}

// TestTheTimeBudgetIsReportedAsItsOwnBound separates the two deadlines. A
// caller whose own request timed out and an operator whose extraction bound is
// too tight have different problems and different fixes, so the refusal says
// which one fired.
func TestTheTimeBudgetIsReportedAsItsOwnBound(t *testing.T) {
	extractor := document.NewExtractor(document.Limits{MaxDuration: time.Nanosecond})
	_, err := extractor.Extract(t.Context(), document.Input{
		Data:   fixture(t, "many-pages.pdf"),
		Format: "pdf",
	})
	require.ErrorIs(t, err, document.ErrTimeBudgetExceeded)
}

// TestACanceledCallerStopsTheExtraction keeps the caller's own cancellation
// distinct from the bound above. This one reports the caller's error, because
// the extraction did not run out of budget, the request went away.
func TestACanceledCallerStopsTheExtraction(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := document.NewExtractor(document.Limits{}).Extract(ctx, document.Input{
		Data:   fixture(t, "many-pages.pdf"),
		Format: "pdf",
	})
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, document.ErrTimeBudgetExceeded)
}

// TestBytesThatAreNotTheDeclaredFormatAreRefused holds the mismatch rule. The
// caller's claim is the thing that failed, so the refusal names the mismatch
// rather than whatever the bytes turned out to be.
func TestBytesThatAreNotTheDeclaredFormatAreRefused(t *testing.T) {
	for _, testCase := range []struct {
		name string
		data []byte
	}{
		{"a PNG wearing a pdf label", []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 64))},
		{"plain text", []byte("This is not a PDF at all.\n")},
		{"a PDF header that starts late", append([]byte("junk"), fixture(t, "handwritten.pdf")...)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := extract(t, document.Input{Data: testCase.data, Format: "pdf"})
			require.ErrorIs(t, err, document.ErrFormatMismatch)
		})
	}
}

// TestAnUndeclaredFormatIsReadFromTheBytes lets a caller that named no
// container still get an extraction, and refuses one whose bytes no engine
// here reads. The two answers are different errors because the fixes are
// different: one caller attached the wrong file, the other attached a file
// this gateway does not handle.
func TestAnUndeclaredFormatIsReadFromTheBytes(t *testing.T) {
	extraction, err := extract(t, document.Input{Data: fixture(t, "handwritten.pdf")})
	require.NoError(t, err)
	require.Contains(t, extraction.Text, "Starport native extraction proof.")

	_, err = extract(t, document.Input{Data: []byte("PK\x03\x04 not a pdf")})
	require.ErrorIs(t, err, document.ErrUnsupportedFormat)
}

// TestTheDeclaredFormatIsSpelledSeveralWays keeps the three spellings the
// codecs and the file store already use from becoming three refusals.
func TestTheDeclaredFormatIsSpelledSeveralWays(t *testing.T) {
	for _, spelling := range []string{
		"pdf", "PDF", ".pdf", "application/pdf", "application/pdf; charset=binary", " pdf ",
	} {
		_, err := extract(t, document.Input{Data: fixture(t, "handwritten.pdf"), Format: spelling})
		require.NoErrorf(t, err, "spelling %q", spelling)
	}

	_, err := extract(t, document.Input{Data: fixture(t, "handwritten.pdf"), Format: "docx"})
	require.ErrorIs(t, err, document.ErrUnsupportedFormat)
}

// TestMalformedBytesAreRefusedRatherThanCrashing is the load-bearing safety
// test. The reader signals malformed input by panicking, and these bytes are a
// caller's own upload. A panic that escaped would let one caller decide
// whether other callers get served.
//
// The last case is the one that proves the recovery rather than the error
// return: driven straight at the reader, a page count that is not a number
// panics with `unexpected keyword "??" parsing object`.
func TestMalformedBytesAreRefusedRatherThanCrashing(t *testing.T) {
	whole := fixture(t, "handwritten.pdf")
	for _, testCase := range []struct {
		name string
		data []byte
	}{
		{"a header and nothing else", []byte("%PDF-1.4\n")},
		{"truncated before the trailer", whole[:len(whole)/2]},
		{"a corrupted cross-reference table", corrupt(whole, "xref", "xrEf")},
		{"a page count that is not a number", corrupt(whole, "/Count 1", "/Count ??")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := extract(t, document.Input{Data: testCase.data, Format: "pdf"})
				require.ErrorIs(t, err, document.ErrMalformedDocument)
			})
		})
	}
}

// TestAnEmptyDocumentIsRefused answers before the reader is opened. Zero bytes
// carry no format to sniff, so the refusal names the emptiness instead.
func TestAnEmptyDocumentIsRefused(t *testing.T) {
	_, err := extract(t, document.Input{Data: nil, Format: "pdf"})
	require.ErrorIs(t, err, document.ErrEmptyDocument)
}

// TestARefusalNamesTheDocument matters when a caller attached several files.
// The filename is the only name the caller gave any of them, and a refusal
// that omits it makes the caller guess which upload failed.
func TestARefusalNamesTheDocument(t *testing.T) {
	_, err := extract(t, document.Input{
		Data:     []byte("not a pdf"),
		Format:   "pdf",
		Filename: "quarterly-report.pdf",
	})
	require.ErrorContains(t, err, "quarterly-report.pdf")
	require.ErrorIs(t, err, document.ErrFormatMismatch)
}

// TestAnUnsetBoundTakesItsDefault keeps a partly configured extractor working.
// An operator who set only the page bound should not get an extractor whose
// time bound is zero and therefore refuses everything.
func TestAnUnsetBoundTakesItsDefault(t *testing.T) {
	extractor := document.NewExtractor(document.Limits{MaxPages: 3})
	require.Equal(t, 3, extractor.Limits().MaxPages)
	require.Equal(t, document.DefaultLimits().MaxDuration, extractor.Limits().MaxDuration)

	extraction, err := extractor.Extract(t.Context(), document.Input{
		Data:   fixture(t, "handwritten.pdf"),
		Format: "pdf",
	})
	require.NoError(t, err)
	require.Equal(t, 1, extraction.PageCount())
}

func corrupt(data []byte, from, to string) []byte {
	return []byte(strings.Replace(string(data), from, to, 1))
}
