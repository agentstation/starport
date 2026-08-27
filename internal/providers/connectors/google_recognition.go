package connectors

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// Gemini has no recognition path. It reads a scanned page through the same
// generate call that answers a chat turn, so this transport builds a chat
// request and reads the answer back as pages.
//
// That shape has one real hazard. A chat model returns prose, and prose has no
// page boundary in it. A provider that stops early therefore returns a shorter
// document rather than an error, and nothing in the answer says which of the
// two happened. The marker below is what makes the boundary readable, and the
// page count the request carries is what makes a short answer visible to the
// caller that ordered the read.

// recognitionPageMarker introduces one page in the model's answer. It is
// written to be text no document contains: a caller cannot make a page whose
// own text collides with it without knowing this constant.
const recognitionPageMarker = "<<<STARPORT_PAGE_"

const recognitionMarkerSuffix = ">>>"

// recognitionMediaType names bytes whose upload stated no type. The catalog
// serves this operation for PDF alone, so a document that arrived without a
// stated type is one.
const recognitionMediaType = "application/pdf"

// recognitionInstruction tells the model to transcribe rather than to read.
// The difference matters: a model asked what a document says answers with a
// summary, and a summary that reached the chat turn would look exactly like a
// transcript of a short document.
const recognitionInstruction = `Transcribe the attached document.

Write every page in page order. Before each page, write a line containing only
` + recognitionPageMarker + `N` + recognitionMarkerSuffix + `, where N is that page's one-based number.
Write a marker for every page, including a page that carries no text.
Reproduce what each page shows. Do not summarize it, translate it, or add
commentary of your own.`

// RecognizeDocument reads the text off one document's pages.
func (c *GoogleAIStudioConnector) RecognizeDocument(
	ctx context.Context,
	request *RecognitionRequest,
) (*RecognitionResponse, error) {
	if request == nil || len(request.Document.Bytes) == 0 {
		return nil, fmt.Errorf("%w: recognition request carries no document", ErrInvalidMediaRequest)
	}

	chat := &ChatRequest{
		Model:      request.Model,
		Endpoint:   request.Endpoint,
		Credential: request.Credential,
		Messages: []Message{{
			Role: RoleUser,
			Content: []ContentPart{
				{Type: contentTypeText, Text: recognitionPrompt(request.Pages)},
				{Type: contentTypeFile, File: &File{
					Filename: request.Document.Filename,
					FileData: recognitionDataURL(request.Document),
				}},
			},
		}},
	}

	answer, err := c.googleBaseConnector.Chat(ctx, chat, c.getEndpoint, c.setHeaders)
	if err != nil {
		return nil, err
	}
	if answer == nil || len(answer.Choices) == 0 {
		return nil, fmt.Errorf("%w: recognition answer carries no content", ErrInvalidMediaRequest)
	}

	response := &RecognitionResponse{Pages: recognizedPages(contentText(answer.Choices[0].Message.Content))}
	if answer.Usage.TotalTokens > 0 || answer.Usage.PromptTokens > 0 {
		response.Usage = &MediaUsage{
			InputTokens:  answer.Usage.PromptTokens,
			OutputTokens: answer.Usage.CompletionTokens,
			TotalTokens:  answer.Usage.TotalTokens,
		}
	}
	return response, nil
}

// recognitionPrompt states the instruction, and states the page count when the
// caller counted one. Telling the model how many pages it must produce is the
// cheapest way to stop it from stopping early.
func recognitionPrompt(pages int) string {
	if pages <= 0 {
		return recognitionInstruction
	}
	return recognitionInstruction + "\n\nThe document has " + strconv.Itoa(pages) + " pages."
}

// recognitionDataURL renders the upload as the inline data the Gemini part
// builder already understands.
func recognitionDataURL(document UploadedFile) string {
	mediaType := document.MediaType
	if mediaType == "" {
		mediaType = recognitionMediaType
	}
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(document.Bytes)
}

// recognizedPages splits the model's answer at the page markers.
//
// Text before the first marker is dropped. A model sometimes opens with a
// sentence about what it is doing, and that sentence belongs to no page.
//
// A marker whose number does not parse is treated as page text rather than as
// a boundary, so a document that quotes something marker-shaped does not gain
// a page.
func recognizedPages(answer string) []RecognizedPage {
	var pages []RecognizedPage
	var current *strings.Builder

	for _, line := range strings.Split(answer, "\n") {
		number, marker := recognitionPageNumber(line)
		if marker {
			pages = append(pages, RecognizedPage{Number: number})
			current = &strings.Builder{}
			continue
		}
		if current == nil {
			continue
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
		pages[len(pages)-1].Text = strings.TrimSpace(current.String())
	}
	return pages
}

// recognitionPageNumber reads the page number off a marker line.
func recognitionPageNumber(line string) (int, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, recognitionPageMarker) ||
		!strings.HasSuffix(trimmed, recognitionMarkerSuffix) {
		return 0, false
	}
	digits := trimmed[len(recognitionPageMarker) : len(trimmed)-len(recognitionMarkerSuffix)]
	number, err := strconv.Atoi(digits)
	if err != nil || number <= 0 {
		return 0, false
	}
	return number, true
}
