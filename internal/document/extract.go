package document

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

// Extractor reads text out of a document inside this process.
//
// It holds bounds and nothing else. There is no client, no cache, and no
// account: PLG5 owns the cache and scopes it to the account that paid, and this
// seam stays the part that turns bytes into text.
type Extractor struct {
	limits Limits
}

// NewExtractor returns an extractor bounded by the given limits. An unset
// bound takes its default.
func NewExtractor(limits Limits) *Extractor {
	return &Extractor{limits: limits.normalize()}
}

// Limits returns the bounds this extractor applies.
func (e *Extractor) Limits() Limits { return e.limits }

// Extract reads the document's text layer.
//
// The order of the checks is the point. A document is refused for its bytes
// before it is opened, refused for its page count before a page is read, and
// only then read page by page against the deadline. Each step that runs is
// paid for by the step before it having narrowed the input.
//
// A document that opens and yields no usable text is not an error. It is a
// scanned document, and reporting it as one is what lets the recognition
// engine take over.
func (e *Extractor) Extract(ctx context.Context, input Input) (Extraction, error) {
	if len(input.Data) == 0 {
		return Extraction{}, e.refuse(input, ErrEmptyDocument)
	}

	declared, known := declaredFormat(input.Format)
	if !known {
		return Extraction{}, e.refuse(input, fmt.Errorf("%w: %q", ErrUnsupportedFormat, input.Format))
	}
	sniffed, recognized := sniffFormat(input.Data)
	switch {
	case declared != "" && declared != sniffed:
		// The caller named a container these bytes are not. That is a
		// mismatch whether or not this engine recognizes what they are: the
		// caller's own claim is the thing that failed.
		return Extraction{}, e.refuse(input, fmt.Errorf("%w: declared %q, read %q",
			ErrFormatMismatch, declared, formatOrUnknown(sniffed, recognized)))
	case !recognized:
		return Extraction{}, e.refuse(input, ErrUnsupportedFormat)
	}

	deadline, cancel := context.WithTimeout(ctx, e.limits.MaxDuration)
	defer cancel()

	extraction, err := readPDF(deadline, bytes.NewReader(input.Data), int64(len(input.Data)), e.limits)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			// The extractor's own bound fired rather than the caller's. Say
			// so, because the two have different owners and different fixes.
			err = fmt.Errorf("%w after %s", ErrTimeBudgetExceeded, e.limits.MaxDuration)
		}
		return Extraction{}, e.refuse(input, err)
	}
	return extraction, nil
}

// refuse names the document a refusal is about. A caller that attached three
// files needs to know which one failed, and the filename is the only name it
// gave any of them.
func (e *Extractor) refuse(input Input, err error) error {
	if input.Filename == "" {
		return err
	}
	return fmt.Errorf("document %q: %w", input.Filename, err)
}

// sniffFormat reads the container out of the bytes themselves.
//
// The PDF header sits at the start of a conforming file. Readers that hunt for
// it deeper accept files built to look like something else, and this engine
// runs on a caller's upload, so it does not hunt.
func sniffFormat(data []byte) (Format, bool) {
	if bytes.HasPrefix(data, []byte("%PDF-")) {
		return FormatPDF, true
	}
	return "", false
}

// formatOrUnknown renders what the bytes turned out to be for a refusal.
func formatOrUnknown(sniffed Format, recognized bool) string {
	if !recognized {
		return "an unrecognized format"
	}
	return string(sniffed)
}
