// Package document turns an attached document into text inside this process.
//
// This is the native parser engine. It reaches no provider and spends no
// money, which is the whole reason it exists: a document that already carries
// a text layer needs no model to read it, and sending one to a recognition
// provider would pay for work this process can do.
//
// The package is a leaf. It holds no provider client, no transport, and no
// Starport concept beyond the bytes it was handed and the text it read back.
// A dependency in either direction would let the meaning of a request decide
// what a document says, and the import graph test holds the rule.
//
// The bytes arriving here are a caller's own upload, so every read is bounded.
// A page count bound and an elapsed time bound cap the work, a decode failure
// inside the reader becomes a typed refusal rather than a panic, and the
// stored-byte bound that caps the input itself belongs to internal/files.
package document

import (
	"errors"
	"strings"
	"time"
)

// Format names a document container the native engine can read.
type Format string

// FormatPDF is the one container this engine reads. Starmap spells the
// modality "pdf", and the OpenRouter wire word for it is "file".
const FormatPDF Format = "pdf"

var (
	// ErrEmptyDocument reports a document that carries no bytes.
	ErrEmptyDocument = errors.New("document carries no bytes")
	// ErrUnsupportedFormat reports a container the native engine cannot read.
	// A caller reaching this has attached something no engine here handles,
	// which is different from a document whose text this engine cannot find.
	ErrUnsupportedFormat = errors.New("document format is not read by the native engine")
	// ErrFormatMismatch reports bytes that are not the container the caller
	// declared. Reading them anyway would let a caller drive this engine to a
	// parser it did not name.
	ErrFormatMismatch = errors.New("document bytes are not the declared format")
	// ErrMalformedDocument reports bytes the reader could not parse. The
	// reader panics on some malformed input, and this turns that into an
	// answer the caller can act on.
	ErrMalformedDocument = errors.New("document is malformed")
	// ErrPageBudgetExceeded reports a document with more pages than the
	// configured bound. The refusal comes before any page is read, so a
	// thousand-page upload costs the gateway one header parse.
	ErrPageBudgetExceeded = errors.New("document exceeds the page budget")
	// ErrTimeBudgetExceeded reports an extraction that ran past its elapsed
	// time bound. A document can be small and still be expensive to read.
	ErrTimeBudgetExceeded = errors.New("document extraction exceeded its time budget")
	// ErrRecognitionFailed reports that the recognition engine did not return
	// every page of a document. It lives beside the native engine's refusals
	// because a caller reads one document vocabulary, not one per engine, and
	// the two engines fail the same request for the same reason: the gateway
	// was asked to turn a document into text and did not.
	//
	// The native engine never raises it. The seam that ordered the recognition
	// read does, because it is the seam that knows how many pages the document
	// holds and therefore whether the answer is the whole document.
	ErrRecognitionFailed = errors.New("document recognition failed")
)

// Input is one document handed to the native engine.
type Input struct {
	// Data holds the document bytes.
	Data []byte

	// Format is the container the caller declared, spelled either as a bare
	// name such as "pdf" or as a media type such as "application/pdf". An
	// empty value asks this engine to read the container out of the bytes.
	Format string

	// Filename is the caller's own name for the document. It never decides
	// the container, because a name is not evidence about bytes. It travels
	// so a caller with several attachments learns which one a refusal names.
	Filename string
}

// Page is one page of a document after extraction.
type Page struct {
	// Number is the page's one-based position in the document.
	Number int
	// Text is the text this page carried, with trailing space removed from
	// each line. It is empty when the page carried no text layer.
	Text string
	// Scanned reports that this page carried no usable text layer, so a
	// model reading the document natively would see nothing here.
	Scanned bool
}

// Extraction is what the native engine read out of one document.
type Extraction struct {
	// Text is every page's text in page order, separated by a blank line.
	Text string
	// Pages holds one entry per page in the document, in page order. Its
	// length is the document's page count, because the page budget refuses
	// an oversized document before any page is read.
	Pages []Page
	// Scanned reports that no page carried a usable text layer. This is the
	// signal that the document needs the recognition engine: the native read
	// succeeded and found nothing a model could use.
	Scanned bool
}

// PageCount returns the number of pages the document holds.
func (e Extraction) PageCount() int { return len(e.Pages) }

// usableTextRunes is how many letters or digits a page must carry before this
// engine calls its text layer usable.
//
// A pure emptiness test fails in the wrong direction. A scanned page often
// carries one stray glyph from a watermark or a font encoding artifact, and
// calling that a text layer would hand a model a page of pixels as a nearly
// empty string. Refusing to call it one costs a recognition pass on a page
// that held nothing worth reading anyway.
const usableTextRunes = 8

// Limits bounds one extraction.
type Limits struct {
	// MaxPages refuses a document with more pages than this.
	MaxPages int
	// MaxDuration refuses an extraction that runs longer than this.
	MaxDuration time.Duration
}

// DefaultLimits returns the bounds this gateway applies when an operator
// configures none.
//
// The page bound is generous for a document a caller attaches to a chat turn
// and small enough that one upload cannot occupy a worker. The time bound is
// well past what a healthy read of that many pages takes, so it fires on a
// document built to be slow rather than on a large one.
func DefaultLimits() Limits {
	return Limits{MaxPages: 200, MaxDuration: 15 * time.Second}
}

// normalize fills an unset bound with its default. A zero bound means "not
// configured" rather than "no work allowed": an operator who set only the page
// bound should not get an extraction that always times out.
func (l Limits) normalize() Limits {
	defaults := DefaultLimits()
	if l.MaxPages <= 0 {
		l.MaxPages = defaults.MaxPages
	}
	if l.MaxDuration <= 0 {
		l.MaxDuration = defaults.MaxDuration
	}
	return l
}

// declaredFormat reads the container the caller named. It accepts the bare
// name, a media type, and a filename extension, because the two protocol
// families and the file store each spell the same container differently.
func declaredFormat(declared string) (Format, bool) {
	normalized := strings.ToLower(strings.TrimSpace(declared))
	if normalized == "" {
		return "", true
	}
	normalized = strings.TrimPrefix(normalized, ".")
	if index := strings.Index(normalized, ";"); index >= 0 {
		normalized = strings.TrimSpace(normalized[:index])
	}
	switch normalized {
	case "pdf", "application/pdf", "application/x-pdf":
		return FormatPDF, true
	default:
		return "", false
	}
}

// usableText reports whether a page's text is a text layer rather than noise.
func usableText(text string) bool {
	count := 0
	for _, character := range text {
		switch {
		case character >= '0' && character <= '9':
			count++
		case character >= 'a' && character <= 'z':
			count++
		case character >= 'A' && character <= 'Z':
			count++
		case character > 0x7F && !isSpace(character):
			// A non-ASCII letter counts too. This engine reads documents in
			// languages whose alphabets are outside the ranges above, and
			// calling those pages scanned would send them to a model that
			// would only read back what the text layer already held.
			count++
		}
		if count >= usableTextRunes {
			return true
		}
	}
	return false
}

func isSpace(character rune) bool {
	switch character {
	case ' ', '\t', '\n', '\r', '\v', '\f', 0x00A0, 0x2007, 0x202F:
		return true
	default:
		return false
	}
}
