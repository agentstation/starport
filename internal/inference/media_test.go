package inference

import (
	"testing"
)

// TestEveryContentKindIsCounted fails when a new content kind ships without a
// media counter. That failure is the point: an uncounted media part reaches
// routing as a text-only request, so a model that cannot read it still looks
// like a valid route.
func TestEveryContentKindIsCounted(t *testing.T) {
	samples := contentSamples()
	for _, kind := range ContentKinds() {
		units := EstimateMediaUnits([]Message{{
			Role:    RoleUser,
			Content: []ContentPart{samples[kind]},
		}})
		if kind == ContentText {
			if units.Total() != 0 {
				t.Fatalf("text counted as %d media unit(s)", units.Total())
			}
			continue
		}
		if units.Total() != 1 {
			t.Fatalf("content kind %q counted as %d media unit(s)", kind, units.Total())
		}
	}
}

// TestMediaUnitsCountInlineBytes holds the distinction the counter exists to
// make. Inline bytes are bytes the gateway carried; a remote reference is a
// promise the gateway never read, so counting it would overstate the request.
func TestMediaUnitsCountInlineBytes(t *testing.T) {
	units := EstimateMediaUnits([]Message{{
		Role: RoleUser,
		Content: []ContentPart{
			{Kind: ContentAudio, Audio: &Audio{Data: []byte{1, 2, 3, 4}, Format: "wav"}},
			{Kind: ContentVideo, Video: &Video{URL: "https://example.invalid/clip.mp4"}},
		},
	}})

	if units.Audio != 1 || units.Videos != 1 {
		t.Fatalf("counts = %+v", units)
	}
	if units.InlineBytes != 4 {
		t.Fatalf("inline bytes = %d, want 4", units.InlineBytes)
	}
}

// TestRequestMediaModalitiesOmitsText proves the list names media alone.
// Every chat model reads text, so a text entry would only put a modality
// check in front of traffic that already works.
func TestRequestMediaModalitiesOmitsText(t *testing.T) {
	samples := contentSamples()
	messages := []Message{{Role: RoleUser, Content: []ContentPart{
		samples[ContentText],
		samples[ContentAudio],
		samples[ContentImage],
	}}}

	modalities := RequestMediaModalities(messages)
	want := []Modality{ModalityImage, ModalityAudio}
	if len(modalities) != len(want) {
		t.Fatalf("modalities = %v, want %v", modalities, want)
	}
	for index, modality := range want {
		if modalities[index] != modality {
			t.Fatalf("modalities = %v, want %v", modalities, want)
		}
	}

	if plain := RequestMediaModalities([]Message{{
		Role:    RoleUser,
		Content: []ContentPart{samples[ContentText]},
	}}); plain != nil {
		t.Fatalf("a text request named modalities %v", plain)
	}
}

// TestMediaCountsReadThePayloadNotTheKind holds the tolerance the older call
// sites need. A part built with a payload and no kind is still media, and
// treating it as text would route an image to a model that cannot see.
func TestMediaCountsReadThePayloadNotTheKind(t *testing.T) {
	units := EstimateMediaUnits([]Message{{
		Role:    RoleUser,
		Content: []ContentPart{{Image: &Image{URL: "https://example.invalid/cat.png"}}},
	}})
	if units.Images != 1 {
		t.Fatalf("images = %d, want 1", units.Images)
	}
}
