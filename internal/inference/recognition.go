package inference

import "strings"

// Recognition is the one operation the gateway asks for on its own initiative.
// Every other operation the router plans came from a path a caller chose. A
// recognition call happens inside a chat turn, because the caller attached a
// document whose pages carry no text and named the engine that reads them.
//
// It still needs canonical types of its own. The pages a recognizer returns
// are cached and billed by their own seams, and both read the shape below
// rather than one provider's answer.

// RecognitionRequest asks a model to read the text off every page of one
// document.
type RecognitionRequest struct {
	Model string
	// Document is the whole document rather than one page. Splitting a
	// container into single-page documents needs a writer this gateway does
	// not carry, and a provider that reads a document reads its pages in
	// order anyway.
	Document UploadedFile
	// Pages is how many pages the native read counted. It travels because a
	// recognizer cannot otherwise tell a document that ended from an answer
	// that was cut short, and those two have to reach the caller differently.
	Pages int
}

// Clone returns an independent recognition request copy.
func (r RecognitionRequest) Clone() RecognitionRequest {
	clone := r
	clone.Document = r.Document.Clone()
	return clone
}

// RecognizedPage is the text one page carried.
type RecognizedPage struct {
	// Number is the page's one-based position in the document.
	Number int
	// Text is what the recognizer read on that page. It is empty for a page
	// that held nothing, which is a real answer rather than a failure.
	Text string
}

// RecognitionResponse is the canonical result of reading one document.
//
// The answer is per page rather than one string. A short answer is the failure
// this operation actually has: a provider that stops early returns text for
// the pages it reached, and only a page count separates that from a document
// that ended there.
type RecognitionResponse struct {
	Model string
	// Pages holds one entry per page the provider read, in page order.
	Pages []RecognizedPage
	Usage Usage
}

// Clone returns an independent recognition response copy.
func (r RecognitionResponse) Clone() RecognitionResponse {
	clone := r
	clone.Pages = append([]RecognizedPage(nil), r.Pages...)
	return clone
}

// Text joins every recognized page in page order, separated by a blank line.
// It is the shape the native engine returns for the same document, so a model
// reads one string whichever engine produced it.
func (r RecognitionResponse) Text() string {
	rendered := make([]string, 0, len(r.Pages))
	for _, page := range r.Pages {
		if page.Text == "" {
			continue
		}
		rendered = append(rendered, page.Text)
	}
	return strings.Join(rendered, "\n\n")
}
