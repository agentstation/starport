package connectors

import (
	"encoding/base64"
	"fmt"

	"github.com/agentstation/starport/internal/inference"
)

// audioMediaTypes maps the container names the audio shape uses onto the
// media types a provider expects. The mp3 entry is the reason this map
// exists: "audio/" joined to the container name would spell it wrong.
var audioMediaTypes = map[string]string{
	"wav":   "audio/wav",
	"mp3":   "audio/mpeg",
	"mpeg":  "audio/mpeg",
	"flac":  "audio/flac",
	"ogg":   "audio/ogg",
	"opus":  "audio/opus",
	"aac":   "audio/aac",
	"m4a":   "audio/mp4",
	"webm":  "audio/webm",
	"pcm16": "audio/pcm",
}

// audioMediaType names the media type of one audio container. An unknown
// container keeps its own name, so a provider reports its own contract error
// instead of the gateway guessing on the caller's behalf.
func audioMediaType(format string) string {
	if mediaType, ok := audioMediaTypes[format]; ok {
		return mediaType
	}
	if format == "" {
		return ""
	}
	return "audio/" + format
}

// mediaPayload names the bytes behind one media part, whatever shape carried
// it. A transport that speaks base64 blocks reads Base64, and a transport
// that speaks references reads URL. Exactly one of the two is set.
type mediaPayload struct {
	MediaType string
	Base64    string
	URL       string
}

// partMediaPayload reads the media bytes or reference of one wire content
// part. Every transport that reshapes content reads media through this
// function, so a new part shape reaches all of them at once.
func partMediaPayload(part ContentPart) (mediaPayload, bool) {
	switch {
	case part.ImageURL != nil:
		return dataURLPayload(part.ImageURL.URL), true
	case part.InputAudio != nil:
		return mediaPayload{MediaType: audioMediaType(part.InputAudio.Format), Base64: part.InputAudio.Data}, true
	case part.File != nil:
		return dataURLPayload(part.File.FileData), true
	case part.VideoURL != nil:
		return dataURLPayload(part.VideoURL.URL), true
	}
	return mediaPayload{}, false
}

// dataURLPayload reads inline bytes out of a data URL, or keeps a remote
// reference whole. A remote reference carries no media type, and a provider
// that needs one reports its own error.
func dataURLPayload(url string) mediaPayload {
	if mediaType, data, ok := parseDataURL(url); ok {
		return mediaPayload{MediaType: mediaType, Base64: data}
	}
	return mediaPayload{URL: url}
}

// contentFromInference reshapes one canonical part into the OpenAI-shaped
// wire part. A kind with no arm here returns the named error, so a part never
// reaches a provider as a bare type string with its payload gone.
func contentFromInference(part inference.ContentPart) (ContentPart, error) {
	wire := ContentPart{Type: string(part.Kind), Text: part.Text}
	switch part.Kind {
	case inference.ContentText:
		wire.Type = contentTypeText
	case inference.ContentImage:
		if part.Image == nil {
			return ContentPart{}, fmt.Errorf("%w: image part carries no image", ErrInvalidMessageContent)
		}
		wire.Type = "image_url"
		wire.ImageURL = &ImageURL{URL: part.Image.URL, Detail: part.Image.Detail}
	case inference.ContentAudio:
		if part.Audio == nil || len(part.Audio.Data) == 0 {
			return ContentPart{}, fmt.Errorf("%w: audio part carries no inline data", ErrInvalidMessageContent)
		}
		wire.Type = "input_audio"
		wire.InputAudio = &InputAudio{
			Data:   base64.StdEncoding.EncodeToString(part.Audio.Data),
			Format: part.Audio.Format,
		}
	case inference.ContentDocument:
		if part.Document == nil || part.Document.URL == "" {
			return ContentPart{}, fmt.Errorf("%w: document part carries no reference", ErrInvalidMessageContent)
		}
		wire.Type = "file"
		wire.File = &File{Filename: part.Document.Filename, FileData: part.Document.URL}
	case inference.ContentVideo:
		if part.Video == nil || part.Video.URL == "" {
			return ContentPart{}, fmt.Errorf("%w: video part carries no reference", ErrInvalidMessageContent)
		}
		wire.Type = "video_url"
		wire.VideoURL = &VideoURL{URL: part.Video.URL}
	default:
		return ContentPart{}, fmt.Errorf("%w: %q", ErrContentKindUnsupported, part.Kind)
	}
	if part.CacheControl != "" {
		wire.CacheControl = &CacheControl{Type: part.CacheControl}
	}
	return wire, nil
}

// contentToInference reshapes one wire part back into a canonical part. It
// reads the type token rather than the payload pointer, so a part that names
// a media type and carries nothing fails instead of arriving as empty text.
func contentToInference(part ContentPart) (inference.ContentPart, error) {
	canonical := inference.ContentPart{Kind: inference.ContentText, Text: part.Text}
	switch part.Type {
	case "", contentTypeText, "input_text":
	case "image_url", "input_image":
		if part.ImageURL == nil {
			return inference.ContentPart{}, fmt.Errorf("%w: image_url part carries no image", ErrInvalidMessageContent)
		}
		canonical.Kind = inference.ContentImage
		canonical.Image = &inference.Image{URL: part.ImageURL.URL, Detail: part.ImageURL.Detail}
	case "input_audio":
		if part.InputAudio == nil {
			return inference.ContentPart{}, fmt.Errorf("%w: input_audio part carries no audio", ErrInvalidMessageContent)
		}
		data, err := base64.StdEncoding.DecodeString(part.InputAudio.Data)
		if err != nil {
			return inference.ContentPart{}, fmt.Errorf("%w: input_audio.data must be base64", ErrInvalidMessageContent)
		}
		canonical.Kind = inference.ContentAudio
		canonical.Audio = &inference.Audio{Data: data, Format: part.InputAudio.Format}
	case "file", "input_file":
		if part.File == nil {
			return inference.ContentPart{}, fmt.Errorf("%w: file part carries no file", ErrInvalidMessageContent)
		}
		canonical.Kind = inference.ContentDocument
		canonical.Document = &inference.Document{URL: part.File.FileData, Filename: part.File.Filename}
	case "video_url":
		if part.VideoURL == nil {
			return inference.ContentPart{}, fmt.Errorf("%w: video_url part carries no video", ErrInvalidMessageContent)
		}
		canonical.Kind = inference.ContentVideo
		canonical.Video = &inference.Video{URL: part.VideoURL.URL}
	default:
		return inference.ContentPart{}, fmt.Errorf("%w: %q", ErrContentKindUnsupported, part.Type)
	}
	if part.CacheControl != nil {
		canonical.CacheControl = part.CacheControl.Type
	}
	return canonical, nil
}
