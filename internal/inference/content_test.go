package inference

import (
	"reflect"
	"testing"
)

// contentSamples builds one fully populated part for every content kind.
// The clone contract is only as good as the payload it is handed, so each
// sample sets every field its payload owns.
func contentSamples() map[ContentKind]ContentPart {
	return map[ContentKind]ContentPart{
		ContentText: {
			Kind: ContentText,
			Text: "describe this",
		},
		ContentImage: {
			Kind:  ContentImage,
			Image: &Image{URL: "https://example.invalid/image.png", Detail: "high"},
		},
		ContentAudio: {
			Kind: ContentAudio,
			Audio: &Audio{
				URL:    "https://example.invalid/clip.wav",
				Data:   []byte{0x52, 0x49, 0x46, 0x46},
				Format: "wav",
			},
		},
		ContentDocument: {
			Kind: ContentDocument,
			Document: &Document{
				URL:      "https://example.invalid/report.pdf",
				Data:     []byte{0x25, 0x50, 0x44, 0x46},
				Format:   "pdf",
				Filename: "report.pdf",
			},
		},
		ContentVideo: {
			Kind: ContentVideo,
			Video: &Video{
				URL:    "https://example.invalid/clip.mp4",
				Data:   []byte{0x00, 0x00, 0x00, 0x18},
				Format: "mp4",
			},
		},
	}
}

// TestContentKindsAreExhaustive fails when a new ContentKind ships without a
// sample here. That failure is the point: it forces the next kind through the
// clone contract below rather than letting it alias in silence.
func TestContentKindsAreExhaustive(t *testing.T) {
	samples := contentSamples()
	for _, kind := range ContentKinds() {
		sample, ok := samples[kind]
		if !ok {
			t.Fatalf("content kind %q has no clone coverage", kind)
		}
		if sample.Kind != kind {
			t.Fatalf("sample for %q carries kind %q", kind, sample.Kind)
		}
	}
	if len(samples) != len(ContentKinds()) {
		t.Fatalf("samples = %d, content kinds = %d", len(samples), len(ContentKinds()))
	}
}

// TestMessageCloneIsIndependent holds the invariant the retry path depends
// on. A request is cloned before an attempt, so an aliased payload would let
// one attempt rewrite the bytes the next attempt sends.
func TestMessageCloneIsIndependent(t *testing.T) {
	samples := contentSamples()
	original := Message{Role: RoleUser}
	for _, kind := range ContentKinds() {
		original.Content = append(original.Content, samples[kind])
	}

	clone := cloneMessage(original)
	before := contentSamples()

	for i := range original.Content {
		mutateContentPart(&original.Content[i])
	}

	for i, kind := range ContentKinds() {
		if !reflect.DeepEqual(clone.Content[i], before[kind]) {
			t.Fatalf("clone of %q changed with its source: %+v", kind, clone.Content[i])
		}
	}
}

// mutateContentPart rewrites every field a part owns, including the bytes
// behind each payload pointer. Assigning a fresh pointer would prove nothing,
// so each arm writes through the pointer the source already holds.
func mutateContentPart(part *ContentPart) {
	part.Text = "changed"
	part.CacheControl = "changed"
	if part.Image != nil {
		part.Image.URL = "changed"
		part.Image.Detail = "changed"
	}
	if part.Audio != nil {
		part.Audio.URL = "changed"
		part.Audio.Format = "changed"
		part.Audio.Data[0] = 0xFF
	}
	if part.Document != nil {
		part.Document.URL = "changed"
		part.Document.Format = "changed"
		part.Document.Filename = "changed"
		part.Document.Data[0] = 0xFF
	}
	if part.Video != nil {
		part.Video.URL = "changed"
		part.Video.Format = "changed"
		part.Video.Data[0] = 0xFF
	}
}

// TestContentPartPayloadsAreCovered walks the ContentPart fields and fails on
// a pointer payload that mutateContentPart does not reach. Without it, a part
// could gain a fourth payload and still pass the clone test above.
func TestContentPartPayloadsAreCovered(t *testing.T) {
	covered := map[string]bool{
		"Image":    true,
		"Audio":    true,
		"Document": true,
		"Video":    true,
	}
	partType := reflect.TypeOf(ContentPart{})
	for i := 0; i < partType.NumField(); i++ {
		field := partType.Field(i)
		if field.Type.Kind() != reflect.Pointer {
			continue
		}
		if !covered[field.Name] {
			t.Fatalf("ContentPart.%s is a payload with no clone coverage", field.Name)
		}
	}
}
