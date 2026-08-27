package document

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/ledongthuc/pdf"
)

// readPDF opens a PDF and reads each page's text layer.
//
// The reader this calls is a parser for a format a caller controls, and it
// signals malformed input by panicking rather than by returning an error. Every
// call into it therefore runs under recovery, and every recovered panic becomes
// ErrMalformedDocument. A gateway that answered a malformed upload with a
// crashed request would let one caller decide whether other callers get served.
func readPDF(ctx context.Context, source io.ReaderAt, size int64, limits Limits) (Extraction, error) {
	var reader *pdf.Reader
	if err := recovering(func() error {
		opened, err := pdf.NewReader(source, size)
		if err != nil {
			return err
		}
		reader = opened
		return nil
	}); err != nil {
		return Extraction{}, fmt.Errorf("%w: %w", ErrMalformedDocument, err)
	}

	var total int
	if err := recovering(func() error {
		total = reader.NumPage()
		return nil
	}); err != nil {
		return Extraction{}, fmt.Errorf("%w: %w", ErrMalformedDocument, err)
	}
	if total < 0 {
		return Extraction{}, fmt.Errorf("%w: page count %d", ErrMalformedDocument, total)
	}
	if total > limits.MaxPages {
		// Refuse before reading a single page. The page count comes from the
		// document catalog, so a thousand-page upload costs one header parse.
		return Extraction{}, fmt.Errorf("%w: %d pages, limit %d",
			ErrPageBudgetExceeded, total, limits.MaxPages)
	}

	extraction := Extraction{Pages: make([]Page, 0, total), Scanned: true}
	for number := 1; number <= total; number++ {
		// The deadline is checked between pages, which is the only place this
		// loop can stop: the reader has no cancellation of its own.
		if err := ctx.Err(); err != nil {
			return Extraction{}, err
		}
		var page Page
		if err := recovering(func() error {
			page = readPage(reader, number)
			return nil
		}); err != nil {
			return Extraction{}, fmt.Errorf("%w: page %d: %w", ErrMalformedDocument, number, err)
		}
		if !page.Scanned {
			extraction.Scanned = false
		}
		extraction.Pages = append(extraction.Pages, page)
	}
	if total == 0 {
		// A document with no pages carries no text layer, but calling it
		// scanned would send it to a recognition model with nothing to read.
		extraction.Scanned = false
	}
	extraction.Text = joinPages(extraction.Pages)
	return extraction, nil
}

// readPage assembles one page's text. It runs inside recovering.
func readPage(reader *pdf.Reader, number int) Page {
	text := assemble(reader.Page(number).Content().Text)
	return Page{Number: number, Text: text, Scanned: !usableText(text)}
}

// recovering runs fn and turns a panic into an error.
//
// The reader panics on malformed input. recover cannot catch a runtime.Goexit,
// which is what a failed test assertion raises, so this never swallows a test
// failure raised inside fn.
func recovering(fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				err = recoveredErr
				return
			}
			err = fmt.Errorf("%v", recovered)
		}
	}()
	return fn()
}

// joinPages renders the whole document, separating pages with a blank line so
// a model reading the text can tell where one page ended.
func joinPages(pages []Page) string {
	rendered := make([]string, 0, len(pages))
	for _, page := range pages {
		if page.Text == "" {
			continue
		}
		rendered = append(rendered, page.Text)
	}
	return strings.Join(rendered, "\n\n")
}

// sameLineTolerance is how far apart two glyph runs' baselines may sit and
// still belong to the same line, in points. Subscripts and a font switch move
// a baseline by a fraction of a point, and a line break moves it by the
// leading, which is at least the font size.
const sameLineTolerance = 1.0

// wordGapRatio is the fraction of the font size a horizontal gap must exceed
// before it reads as a space.
//
// A PDF stores no spaces. It stores glyph runs at coordinates, and a writer
// that positions each word separately leaves the space implicit in the gap.
// Reading the runs without measuring the gaps concatenates every word on the
// line, which is what a naive extractor returns for a document produced by
// TeX. A model handed that text reads a different document than the caller
// attached.
const wordGapRatio = 0.2

// assemble turns positioned glyph runs into lines of text.
func assemble(runs []pdf.Text) string {
	if len(runs) == 0 {
		return ""
	}
	ordered := make([]pdf.Text, len(runs))
	copy(ordered, runs)
	sort.SliceStable(ordered, func(first, second int) bool {
		if math.Abs(ordered[first].Y-ordered[second].Y) > sameLineTolerance {
			// Y increases upward in PDF space, so a larger Y is higher on the
			// page and therefore earlier in reading order.
			return ordered[first].Y > ordered[second].Y
		}
		return ordered[first].X < ordered[second].X
	})

	var lines []string
	var line strings.Builder
	previous := ordered[0]
	for index, run := range ordered {
		switch {
		case index == 0:
		case math.Abs(run.Y-previous.Y) > sameLineTolerance:
			lines = append(lines, strings.TrimRight(line.String(), " \t"))
			line.Reset()
		case run.X-(previous.X+previous.W) > wordGapRatio*run.FontSize:
			line.WriteByte(' ')
		}
		line.WriteString(run.S)
		previous = run
	}
	lines = append(lines, strings.TrimRight(line.String(), " \t"))

	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
