package inference

// The dedicated media operations are the ones a caller reaches at their own
// path rather than through a chat turn. A chat request carries media as
// content parts; these three carry it as the whole request and the whole
// answer, so each needs its own canonical type.

// UploadedFile is one decoded file a caller sent with a request.
//
// The bytes are held rather than streamed on purpose. A route plan retries
// across providers, and a retry replays the same upload. A reader is consumed
// by the first attempt, so an upload behind one would arrive empty at the
// second, and the caller would see the last provider's rejection instead of an
// answer.
type UploadedFile struct {
	// Filename is the name the caller gave the upload. A provider reads the
	// extension to pick a decoder, so an empty name loses information the
	// caller supplied.
	Filename string
	// MediaType is the content type the caller stated, when it stated one.
	MediaType string
	// Bytes is the decoded payload.
	Bytes []byte
}

// Present reports whether the upload carries a payload.
func (f UploadedFile) Present() bool { return len(f.Bytes) > 0 }

// Clone returns an independent upload copy.
func (f UploadedFile) Clone() UploadedFile {
	clone := f
	clone.Bytes = append([]byte(nil), f.Bytes...)
	return clone
}

// ImagesRequest is the canonical image generation or image edit request. One
// type serves both, because an edit is a generation that starts from a source
// image: the presence of Image is what separates them, and it is also what
// decides the operation the request routes to.
type ImagesRequest struct {
	Model          string
	Prompt         string
	N              int
	Size           string
	Quality        string
	Style          string
	ResponseFormat string
	User           string
	// Image is the picture an edit starts from. An empty value means the
	// request is a generation.
	Image UploadedFile
	// Mask names the region of Image an edit may replace. It is meaningless
	// without Image.
	Mask UploadedFile
}

// IsEdit reports whether the request edits a source image rather than
// generating one. The routed operation follows this answer.
func (r ImagesRequest) IsEdit() bool { return r.Image.Present() }

// Clone returns an independent image request copy.
func (r ImagesRequest) Clone() ImagesRequest {
	clone := r
	clone.Image = r.Image.Clone()
	clone.Mask = r.Mask.Clone()
	return clone
}

// GeneratedImage is one picture an image operation produced. A provider
// answers with inline base64 or with a URL it hosts, never with both, and the
// caller asked for one of the two.
type GeneratedImage struct {
	B64JSON string
	URL     string
	// RevisedPrompt is the prompt the provider actually rendered, when it
	// rewrote the one the caller sent.
	RevisedPrompt string
}

// ImagesResponse is the canonical image operation result.
type ImagesResponse struct {
	Model       string
	CreatedUnix int64
	Images      []GeneratedImage
	Usage       Usage
}

// Clone returns an independent image response copy.
func (r ImagesResponse) Clone() ImagesResponse {
	clone := r
	clone.Images = append([]GeneratedImage(nil), r.Images...)
	return clone
}

// SpeechRequest is the canonical text-to-speech request.
type SpeechRequest struct {
	Model string
	Input string
	Voice string
	// ResponseFormat names the container the caller wants, such as mp3 or
	// wav. A provider decides the default when the caller states none.
	ResponseFormat string
	// Speed multiplies the delivery rate. A nil value means the provider
	// default, which is not the same as 0.
	Speed *float64
}

// Clone returns an independent speech request copy.
func (r SpeechRequest) Clone() SpeechRequest {
	clone := r
	clone.Speed = clonePointer(r.Speed)
	return clone
}

// SpeechResponse is the canonical text-to-speech result. A speech endpoint
// answers with an encoded audio file rather than with JSON, so the bytes and
// their media type are the whole answer.
type SpeechResponse struct {
	Model string
	Audio []byte
	// ContentType is the media type the provider stated for Audio. The
	// gateway repeats it rather than deriving one, because the provider
	// encoded the file.
	ContentType string
	Usage       Usage
}

// Clone returns an independent speech response copy.
func (r SpeechResponse) Clone() SpeechResponse {
	clone := r
	clone.Audio = append([]byte(nil), r.Audio...)
	return clone
}

// TranscriptionRequest is the canonical speech-to-text request. One type
// serves transcription and translation, because the two differ only in the
// language the transcript is written in.
type TranscriptionRequest struct {
	Model string
	File  UploadedFile
	// Language names the language spoken in File, when the caller knows it.
	Language string
	// Prompt supplies vocabulary or context that helps the decoder.
	Prompt string
	// ResponseFormat names the transcript format, such as json, text, srt,
	// or vtt.
	ResponseFormat string
	// Temperature controls decoder sampling. A nil value means the provider
	// default, which is not the same as 0.
	Temperature *float64
	// Translate asks for an English transcript of speech in another
	// language. It selects the translation operation, which a provider
	// exposes at its own path, so it is a routing fact and not a hint.
	Translate bool
}

// Clone returns an independent transcription request copy.
func (r TranscriptionRequest) Clone() TranscriptionRequest {
	clone := r
	clone.File = r.File.Clone()
	clone.Temperature = clonePointer(r.Temperature)
	return clone
}

// TranscriptionResponse is the canonical speech-to-text result.
type TranscriptionResponse struct {
	Model string
	Text  string
	// Language is the language the provider detected, when it reported one.
	Language string
	// Duration is the length of the audio in seconds, when the provider
	// reported it.
	Duration float64
	Usage    Usage
}

// Clone returns an independent transcription response copy.
func (r TranscriptionResponse) Clone() TranscriptionResponse { return r }
