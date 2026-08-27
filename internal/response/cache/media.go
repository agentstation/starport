package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/agentstation/starport/internal/inference"
)

// inlinePrefix marks a media reference that carries its own bytes.
const inlinePrefix = "data:"

// remoteMediaKind names the content kind of the first remote reference one
// part carries, or an empty kind when the part carries none.
//
// A remote reference is a promise, not a payload. The bytes behind
// "https://example.test/clip.wav" can change while the request stays word for
// word the same, so a cached answer would outlive the media it describes.
// Every media kind carries that risk, not the image kind alone.
func remoteMediaKind(part inference.ContentPart) inference.ContentKind {
	switch {
	case part.Image != nil && isRemoteReference(part.Image.URL):
		return inference.ContentImage
	case part.Audio != nil && isRemoteReference(part.Audio.URL):
		return inference.ContentAudio
	case part.Document != nil && isRemoteReference(part.Document.URL):
		return inference.ContentDocument
	case part.Video != nil && isRemoteReference(part.Video.URL):
		return inference.ContentVideo
	default:
		return ""
	}
}

// isRemoteReference reports whether a media reference points outside the
// request. A data URL carries its bytes inline, so the request holds the
// media itself. Anything else names a resource the gateway would fetch.
func isRemoteReference(reference string) bool {
	return reference != "" && !strings.HasPrefix(reference, inlinePrefix)
}

// foldInlineMedia moves every inline media payload out of one request and
// returns the payload digests in walk order. The caller places the digests
// beside the request in the cache-key payload.
//
// The bytes decide the answer, so they must reach the key. They already did:
// the key encoder writes a byte field as base64 and a data URL as itself. The
// digest keeps that true at a fixed size, so a lookup for a request holding a
// large audio file no longer builds a base64 copy of that file.
func foldInlineMedia(request *inference.ChatRequest) []string {
	var digests []string
	for messageIndex := range request.Messages {
		content := request.Messages[messageIndex].Content
		for partIndex := range content {
			digests = foldPart(&content[partIndex], digests)
		}
	}
	return digests
}

// foldPart folds one content part. A part is folded by the payload it holds
// rather than by the kind it names, because a part built with a payload and
// no kind still carries bytes the key must cover.
func foldPart(part *inference.ContentPart, digests []string) []string {
	if part.Image != nil {
		digests, part.Image.URL = foldReference(digests, part.Image.URL)
	}
	if part.Audio != nil {
		digests, part.Audio.URL = foldReference(digests, part.Audio.URL)
		digests, part.Audio.Data = foldBytes(digests, part.Audio.Data)
	}
	if part.Document != nil {
		digests, part.Document.URL = foldReference(digests, part.Document.URL)
		digests, part.Document.Data = foldBytes(digests, part.Document.Data)
	}
	if part.Video != nil {
		digests, part.Video.URL = foldReference(digests, part.Video.URL)
		digests, part.Video.Data = foldBytes(digests, part.Video.Data)
	}
	return digests
}

// foldReference folds one inline data URL. It returns the digest list and the
// reference that stays in the request, which is empty once the bytes moved
// into a digest. A remote reference stays where it is: eligibility refuses the
// request before the fold, and a reference that reached here anyway belongs in
// the key verbatim rather than silently dropped.
func foldReference(digests []string, reference string) ([]string, string) {
	if reference == "" || isRemoteReference(reference) {
		return digests, reference
	}
	return append(digests, mediaDigest([]byte(reference))), ""
}

// foldBytes folds one inline byte payload.
func foldBytes(digests []string, data []byte) ([]string, []byte) {
	if len(data) == 0 {
		return digests, data
	}
	return append(digests, mediaDigest(data)), nil
}

// mediaDigest names one inline payload by its content alone. The kind stays
// out of the digest because the request around it already carries the kind:
// the same bytes sent as audio and as a document sit in differently named
// fields, so the two requests never encode alike.
func mediaDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
