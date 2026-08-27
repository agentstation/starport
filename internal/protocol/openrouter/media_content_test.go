package openrouter

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

// audioBase64 is the raw base64 the audio shape carries. It is not a data
// URL, so the decoder owns the base64 step for audio and no other kind.
var audioBase64 = base64.StdEncoding.EncodeToString([]byte{0x52, 0x49, 0x46, 0x46})

func mediaRequest(part string) string {
	return `{"model":"openai/gpt-4.1","messages":[{"role":"user","content":[` + part + `]}]}`
}

// TestDecodeMediaContentParts holds the wire spelling of each media part. The
// field names below are the contract a caller writes against, so a rename of
// any one of them is a breaking change this test refuses to let pass in
// silence.
func TestDecodeMediaContentParts(t *testing.T) {
	cases := []struct {
		name   string
		part   string
		assert func(t *testing.T, part inference.ContentPart)
	}{
		{
			name: "audio",
			part: `{"type":"input_audio","input_audio":{"data":"` + audioBase64 + `","format":"wav"}}`,
			assert: func(t *testing.T, part inference.ContentPart) {
				require.Equal(t, inference.ContentAudio, part.Kind)
				require.Equal(t, []byte{0x52, 0x49, 0x46, 0x46}, part.Audio.Data)
				require.Equal(t, "wav", part.Audio.Format)
			},
		},
		{
			name: "document",
			part: `{"type":"file","file":{"filename":"report.pdf","file_data":"data:application/pdf;base64,JVBERg=="}}`,
			assert: func(t *testing.T, part inference.ContentPart) {
				require.Equal(t, inference.ContentDocument, part.Kind)
				require.Equal(t, "report.pdf", part.Document.Filename)
				require.Equal(t, "data:application/pdf;base64,JVBERg==", part.Document.URL)
			},
		},
		{
			name: "document under the responses spelling",
			part: `{"type":"input_file","file":{"filename":"a.pdf","file_data":"data:application/pdf;base64,JVBERg=="}}`,
			assert: func(t *testing.T, part inference.ContentPart) {
				require.Equal(t, inference.ContentDocument, part.Kind)
				require.Equal(t, "a.pdf", part.Document.Filename)
			},
		},
		{
			name: "video",
			part: `{"type":"video_url","video_url":{"url":"https://example.test/clip.mp4"}}`,
			assert: func(t *testing.T, part inference.ContentPart) {
				require.Equal(t, inference.ContentVideo, part.Kind)
				require.Equal(t, "https://example.test/clip.mp4", part.Video.URL)
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request, err := DecodeChat(strings.NewReader(mediaRequest(testCase.part)))
			require.NoError(t, err)
			content := request.Inference.Messages[0].Content
			require.Len(t, content, 1)
			testCase.assert(t, content[0])
		})
	}
}

// TestDecodeMediaContentPartErrors proves that a malformed media part fails
// with the index and the field it names. A silent nil payload would reach the
// planner as a part of the right kind carrying nothing.
func TestDecodeMediaContentPartErrors(t *testing.T) {
	cases := []struct {
		name string
		part string
		want string
	}{
		{
			name: "unknown type names the index and the type",
			part: `{"type":"input_smell"}`,
			want: `content[0].type "input_smell" is not supported`,
		},
		{
			name: "audio without data",
			part: `{"type":"input_audio","input_audio":{"format":"wav"}}`,
			want: "content[0].input_audio.data is required",
		},
		{
			name: "audio without format",
			part: `{"type":"input_audio","input_audio":{"data":"` + audioBase64 + `"}}`,
			want: "content[0].input_audio.format is required",
		},
		{
			name: "audio that is not base64",
			part: `{"type":"input_audio","input_audio":{"data":"not base64","format":"wav"}}`,
			want: "content[0].input_audio.data must be base64",
		},
		{
			name: "document under a field name that is not the documented one",
			part: `{"type":"file","file":{"name":"a.pdf","data":"JVBERg=="}}`,
			want: "content[0].file.file_data is required",
		},
		{
			name: "document by reference is refused by name",
			part: `{"type":"file","file":{"file_id":"file-abc"}}`,
			want: "content[0].file.file_id is not supported",
		},
		{
			name: "video without a url",
			part: `{"type":"video_url","video_url":{}}`,
			want: "content[0].video_url.url is required",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := DecodeChat(strings.NewReader(mediaRequest(testCase.part)))
			require.Error(t, err)
			require.Contains(t, err.Error(), testCase.want)
		})
	}
}

// TestMediaPartKeepsCacheControl proves that a cache breakpoint still lands on
// a media part. The breakpoint is applied after the type switch, so a new arm
// that returns early would drop it.
func TestMediaPartKeepsCacheControl(t *testing.T) {
	part := `{"type":"file","file":{"filename":"a.pdf","file_data":"data:application/pdf;base64,JVBERg=="},"cache_control":{"type":"ephemeral"}}`
	request, err := DecodeChat(strings.NewReader(mediaRequest(part)))
	require.NoError(t, err)
	require.Equal(t, "ephemeral", request.Inference.Messages[0].Content[0].CacheControl)
}
