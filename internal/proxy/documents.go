package proxy

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"path/filepath"
	"strings"

	"github.com/agentstation/starport/internal/inference"
)

// defaultDocumentMediaType names bytes whose filename says nothing about them.
// A provider that reads the data URL still gets a well-formed one, and a
// document whose name carries no extension is not a reason to refuse a
// request the caller already stored the bytes for.
const defaultDocumentMediaType = "application/octet-stream"

// StoredDocument is one stored file's bytes and the name it was stored under.
type StoredDocument struct {
	Filename string
	Data     []byte
}

// FileResolver reads one stored document for one account.
//
// This package names the port rather than importing the file service, so the
// concept that owns a stored file keeps its own vocabulary and the proxy keeps
// the one shape it needs.
//
// The tenant argument is not advisory. A resolver reports an identifier
// belonging to another account as absent, exactly as it reports an unknown
// identifier, because an answer that distinguished the two would tell one
// account which identifiers another account holds.
type FileResolver interface {
	ResolveDocument(ctx context.Context, tenant, id string) (StoredDocument, bool, error)
}

// resolveDocuments returns the request the router runs, with every stored
// document reference replaced by the bytes behind it.
//
// It runs once for each request, ahead of the attempt loop. Resolving inside
// that loop would read the same file again for every retry, and a file that
// expired between two attempts would let one request send two different
// documents to two providers.
//
// The bytes land in the same field an inline document uses, so a stored
// reference and an inline upload of the same file reach a provider as the same
// request. Nothing downstream learns which of the two the caller sent.
func (p *proxy) resolveDocuments(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionRequest, error) {
	if !namesStoredDocument(req.Request.Messages) {
		return req, nil
	}
	if p.files == nil {
		return nil, &ValidationError{
			Field:   "messages",
			Message: "this gateway stores no files, so a file_id resolves to nothing",
		}
	}

	resolved := *req
	resolved.Request = req.Request.Clone()
	// One request may name the same file in several parts. The cache holds
	// what this request already read, so the count of reads follows the count
	// of distinct files rather than the count of parts.
	seen := make(map[string]StoredDocument)
	for messageIndex := range resolved.Request.Messages {
		content := resolved.Request.Messages[messageIndex].Content
		for partIndex := range content {
			if err := p.resolvePart(ctx, resolved.TenantID, &content[partIndex], seen); err != nil {
				return nil, err
			}
		}
	}
	return &resolved, nil
}

// resolvePart resolves one content part in place. A part that names no stored
// file is left exactly as it arrived, which is how the inline path stays
// untouched by this seam.
func (p *proxy) resolvePart(
	ctx context.Context,
	tenant string,
	part *inference.ContentPart,
	seen map[string]StoredDocument,
) error {
	if part.Document == nil || part.Document.FileID == "" {
		return nil
	}
	id := part.Document.FileID

	stored, cached := seen[id]
	if !cached {
		var found bool
		var err error
		stored, found, err = p.files.ResolveDocument(ctx, tenant, id)
		if err != nil {
			return fmt.Errorf("resolve stored file %q: %w", id, err)
		}
		if !found {
			return fmt.Errorf("%w: %q", ErrStoredFileNotFound, id)
		}
		seen[id] = stored
	}

	// The filename the caller wrote on the part wins over the stored one. A
	// caller that named the part meant that name to reach the model, and the
	// stored name is the fallback for a part that named nothing.
	filename := part.Document.Filename
	if filename == "" {
		filename = stored.Filename
	}
	part.Document = &inference.Document{
		URL:      documentDataURL(stored),
		Filename: filename,
	}
	return nil
}

// namesStoredDocument reports whether any part of the request names a stored
// file. A request that names none pays nothing for this seam: no clone, no
// map, and no resolver call.
func namesStoredDocument(messages []inference.Message) bool {
	for _, message := range messages {
		for _, part := range message.Content {
			if part.Document != nil && part.Document.FileID != "" {
				return true
			}
		}
	}
	return false
}

// documentDataURL renders stored bytes as the data URL an inline caller would
// have sent. The media type comes from the stored filename, because that is
// the only thing the record says about what the bytes are.
func documentDataURL(stored StoredDocument) string {
	return "data:" + documentMediaType(stored.Filename) + ";base64," +
		base64.StdEncoding.EncodeToString(stored.Data)
}

// documentMediaType names the bytes behind one stored filename.
func documentMediaType(filename string) string {
	mediaType := mime.TypeByExtension(filepath.Ext(filename))
	if mediaType == "" {
		return defaultDocumentMediaType
	}
	// TypeByExtension returns the parameters beside the type, and a data URL
	// carries only the type itself before its own encoding parameter.
	if separator := strings.IndexByte(mediaType, ';'); separator >= 0 {
		mediaType = strings.TrimSpace(mediaType[:separator])
	}
	return mediaType
}
