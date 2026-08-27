package cache

import (
	"errors"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/inference"
)

// mediaIdentity builds an eligible chat identity around one content part.
func mediaIdentity(part inference.ContentPart) ChatIdentity {
	return ChatIdentity{
		TenantID:          "tenant",
		CatalogGeneration: "generation",
		Request: inference.ChatRequest{
			Model: "openai/gpt-4o",
			Messages: []inference.Message{{
				Role: inference.RoleUser,
				Content: []inference.ContentPart{
					{Kind: inference.ContentText, Text: "what is in this"},
					part,
				},
			}},
		},
	}
}

// remoteMediaPart builds a part of one media kind that names bytes the
// gateway would have to fetch. Text has no remote form and reports false.
func remoteMediaPart(kind inference.ContentKind) (inference.ContentPart, bool) {
	const reference = "https://example.invalid/payload"
	switch kind {
	case inference.ContentImage:
		return inference.ContentPart{Kind: kind, Image: &inference.Image{URL: reference}}, true
	case inference.ContentAudio:
		return inference.ContentPart{Kind: kind, Audio: &inference.Audio{URL: reference, Format: "wav"}}, true
	case inference.ContentDocument:
		return inference.ContentPart{Kind: kind, Document: &inference.Document{URL: reference, Format: "pdf"}}, true
	case inference.ContentVideo:
		return inference.ContentPart{Kind: kind, Video: &inference.Video{URL: reference, Format: "mp4"}}, true
	default:
		return inference.ContentPart{}, false
	}
}

// inlineMediaPart builds a part of one media kind that carries its own bytes.
func inlineMediaPart(kind inference.ContentKind, payload []byte) (inference.ContentPart, bool) {
	switch kind {
	case inference.ContentImage:
		return inference.ContentPart{Kind: kind, Image: &inference.Image{
			URL: "data:image/png;base64," + string(payload),
		}}, true
	case inference.ContentAudio:
		return inference.ContentPart{Kind: kind, Audio: &inference.Audio{Data: payload, Format: "wav"}}, true
	case inference.ContentDocument:
		return inference.ContentPart{Kind: kind, Document: &inference.Document{
			Data: payload, Format: "pdf", Filename: "report.pdf",
		}}, true
	case inference.ContentVideo:
		return inference.ContentPart{Kind: kind, Video: &inference.Video{Data: payload, Format: "mp4"}}, true
	default:
		return inference.ContentPart{}, false
	}
}

// TestRemoteMediaOfEveryKindIsIneligible holds the rule the cache was missing.
// The image rule was the whole rule, so a remote audio URL cached: the answer
// described bytes the gateway fetched once, and it replayed after those bytes
// changed. A media kind that ships without a rule fails here, because the
// cases come from the content-kind list rather than from a hand-written table.
func TestRemoteMediaOfEveryKindIsIneligible(t *testing.T) {
	for _, kind := range inference.ContentKinds() {
		part, ok := remoteMediaPart(kind)
		if !ok {
			continue
		}
		t.Run(string(kind), func(t *testing.T) {
			_, err := ChatKey(mediaIdentity(part))
			if !errors.Is(err, ErrIneligible) || !errors.Is(err, ErrMutableMedia) {
				t.Fatalf("error = %v", err)
			}
			if !strings.Contains(err.Error(), string(kind)) {
				t.Fatalf("error %q does not name the kind %q", err, kind)
			}
		})
	}
}

// TestInlineMediaKeysOnItsBytes holds the other half of the rule. Bytes the
// caller sent are part of the request, so the request stays cacheable, but
// only while the bytes reach the key: two different recordings under one
// prompt must not share an answer.
func TestInlineMediaKeysOnItsBytes(t *testing.T) {
	for _, kind := range inference.ContentKinds() {
		first, ok := inlineMediaPart(kind, []byte("payload-one"))
		if !ok {
			continue
		}
		t.Run(string(kind), func(t *testing.T) {
			firstKey, err := ChatKey(mediaIdentity(first))
			if err != nil {
				t.Fatalf("inline %s was refused: %v", kind, err)
			}

			repeat, _ := inlineMediaPart(kind, []byte("payload-one"))
			repeatKey, err := ChatKey(mediaIdentity(repeat))
			if err != nil {
				t.Fatal(err)
			}
			if repeatKey != firstKey {
				t.Fatal("the same bytes produced two keys")
			}

			other, _ := inlineMediaPart(kind, []byte("payload-two"))
			otherKey, err := ChatKey(mediaIdentity(other))
			if err != nil {
				t.Fatal(err)
			}
			if otherKey == firstKey {
				t.Fatalf("different %s bytes shared one key", kind)
			}
		})
	}
}

// TestFoldingLeavesNoBytesInTheKeyedRequest holds the cost rule. Without the
// fold, a lookup for a request holding a large audio file encodes that file as
// base64 before hashing it, so a cache read allocates a copy of the media on
// every request that carries it.
func TestFoldingLeavesNoBytesInTheKeyedRequest(t *testing.T) {
	request := inference.ChatRequest{Messages: []inference.Message{{
		Role: inference.RoleUser,
		Content: []inference.ContentPart{
			{Kind: inference.ContentAudio, Audio: &inference.Audio{Data: []byte("recording"), Format: "wav"}},
			{Kind: inference.ContentDocument, Document: &inference.Document{
				URL: "data:application/pdf;base64,cGFnZQ==", Format: "pdf",
			}},
		},
	}}}

	digests := foldInlineMedia(&request)
	if len(digests) != 2 || digests[0] == digests[1] {
		t.Fatalf("digests = %v", digests)
	}
	content := request.Messages[0].Content
	if len(content[0].Audio.Data) != 0 {
		t.Fatal("audio bytes stayed in the keyed request")
	}
	if content[1].Document.URL != "" {
		t.Fatal("the inline document URL stayed in the keyed request")
	}
}

// TestTextOnlyKeyIsPinnedToItsVersion guards the deployed cache. The key
// payload embeds inference.ChatRequest, so a field added to that canonical
// struct moves this digest even when no caller sets the field, and every
// entry a running gateway holds stops answering its own request.
//
// A digest change is allowed. It is not allowed silently: raise
// SemanticKeyVersion in the same change, so the stale entries keep the prefix
// that wrote them, then update the constant below to the new full key.
func TestTextOnlyKeyIsPinnedToItsVersion(t *testing.T) {
	key, err := ChatKey(ChatIdentity{
		TenantID:          "tenant",
		CatalogGeneration: "generation",
		Request: inference.ChatRequest{
			Model: "openai/gpt-4.1",
			Messages: []inference.Message{{
				Role:    inference.RoleUser,
				Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "hello"}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	const pinned = "responsecache:v5:chat:" +
		"23c80fde6998d446aa8b73c3da6ea6e409355ff82db9a5b14d3c24f1e9168070"
	if key != pinned {
		t.Fatalf("key = %q, want %q; if this change is deliberate, raise SemanticKeyVersion with it", key, pinned)
	}
}
