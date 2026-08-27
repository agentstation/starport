package connectors

import "testing"

func TestParseImageDataURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		mediaType string
		data      string
		ok        bool
	}{
		{"png data url", "data:image/png;base64,AAAA", "image/png", "AAAA", true},
		{"jpeg data url", "data:image/jpeg;base64,QkJC", "image/jpeg", "QkJC", true},
		{"remote url", "https://example.com/cat.png", "", "", false},
		{"missing base64 marker", "data:image/png,AAAA", "", "", false},
		{"missing comma", "data:image/png;base64", "", "", false},
		{"empty media type", "data:;base64,AAAA", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mediaType, data, ok := parseDataURL(tt.url)
			if ok != tt.ok || mediaType != tt.mediaType || data != tt.data {
				t.Fatalf("parseDataURL(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.url, mediaType, data, ok, tt.mediaType, tt.data, tt.ok)
			}
		})
	}
}

func TestContentText(t *testing.T) {
	if got := contentText("plain string"); got != "plain string" {
		t.Fatalf("string content: got %q", got)
	}
	mixed := []ContentPart{
		{Type: contentTypeText, Text: "first"},
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"}},
		{Type: contentTypeText, Text: "second"},
	}
	if got := contentText(mixed); got != "first\n\nsecond" {
		t.Fatalf("mixed content: got %q", got)
	}
}

// TestConvertToGeminiRequestMultimodal proves the Google converter accepts
// OpenAI-style content parts instead of panicking on the string assertion.
func TestConvertToGeminiRequestMultimodal(t *testing.T) {
	c := &googleBaseConnector{}
	req := &ChatRequest{
		Model: "gemini-2.0-flash",
		Messages: []Message{
			{Role: RoleSystem, Content: "Be brief."},
			{Role: RoleUser, Content: []ContentPart{
				{Type: contentTypeText, Text: "What is in this image?"},
				{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"}},
			}},
		},
	}

	got := c.convertToGeminiRequest(req)
	contents := got["contents"].([]map[string]any)
	if len(contents) != 1 {
		t.Fatalf("contents length = %d, want 1", len(contents))
	}
	parts := contents[0]["parts"].([]map[string]any)
	if len(parts) != 2 {
		t.Fatalf("parts length = %d, want 2", len(parts))
	}
	if text := parts[0][contentTypeText].(string); text != "Be brief.\n\nWhat is in this image?" {
		t.Fatalf("system prepend: got %q", text)
	}
	inline := parts[1]["inline_data"].(map[string]any)
	if inline["mime_type"] != "image/png" || inline["data"] != "AAAA" {
		t.Fatalf("inline_data = %v", inline)
	}
}

// TestConvertToGeminiRequestImageFirst covers the system prepend when the
// first user part is an image: the system text gets its own text part.
func TestConvertToGeminiRequestImageFirst(t *testing.T) {
	c := &googleBaseConnector{}
	req := &ChatRequest{
		Model: "gemini-2.0-flash",
		Messages: []Message{
			{Role: RoleSystem, Content: "Be brief."},
			{Role: RoleUser, Content: []ContentPart{
				{Type: "image_url", ImageURL: &ImageURL{URL: "https://example.com/cat.png"}},
			}},
		},
	}

	got := c.convertToGeminiRequest(req)
	parts := got["contents"].([]map[string]any)[0]["parts"].([]map[string]any)
	if len(parts) != 2 {
		t.Fatalf("parts length = %d, want 2", len(parts))
	}
	if text := parts[0][contentTypeText].(string); text != "Be brief." {
		t.Fatalf("leading system part: got %q", text)
	}
	fileData := parts[1]["file_data"].(map[string]any)
	if fileData["file_uri"] != "https://example.com/cat.png" {
		t.Fatalf("file_data = %v", fileData)
	}
}

// TestConvertToGeminiRequestStringContent keeps the plain string path stable.
func TestConvertToGeminiRequestStringContent(t *testing.T) {
	c := &googleBaseConnector{}
	req := &ChatRequest{
		Model: "gemini-2.0-flash",
		Messages: []Message{
			{Role: RoleUser, Content: "hello"},
			{Role: RoleAssistant, Content: "hi"},
		},
	}

	got := c.convertToGeminiRequest(req)
	contents := got["contents"].([]map[string]any)
	if len(contents) != 2 {
		t.Fatalf("contents length = %d, want 2", len(contents))
	}
	if contents[0]["role"] != RoleUser || contents[1]["role"] != wireModelToken {
		t.Fatalf("roles = %v, %v", contents[0]["role"], contents[1]["role"])
	}
	parts := contents[0]["parts"].([]map[string]any)
	if parts[0][contentTypeText] != "hello" {
		t.Fatalf("user part = %v", parts[0])
	}
}

// TestConvertToAnthropicRequestMultimodal proves the Anthropic converter
// splits a data URL into a real media type and a bare base64 payload.
func TestConvertToAnthropicRequestMultimodal(t *testing.T) {
	req := &ChatRequest{
		Model: "claude-sonnet-4-5",
		Messages: []Message{
			{Role: RoleSystem, Content: "Be brief."},
			{Role: RoleUser, Content: []ContentPart{
				{Type: contentTypeText, Text: "What is in this image?"},
				{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"}},
				{Type: "image_url", ImageURL: &ImageURL{URL: "https://example.com/cat.png"}},
			}},
		},
	}

	got, err := convertToAnthropicRequest(req, true)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got["system"] != "Be brief." {
		t.Fatalf("system = %v", got["system"])
	}
	messages := got[wireFieldMessages].([]map[string]any)
	if len(messages) != 1 {
		t.Fatalf("messages length = %d, want 1", len(messages))
	}
	content := messages[0]["content"].([]map[string]any)
	if len(content) != 3 {
		t.Fatalf("content length = %d, want 3", len(content))
	}
	if content[0][contentTypeText] != "What is in this image?" {
		t.Fatalf("text part = %v", content[0])
	}
	source := content[1]["source"].(map[string]any)
	if source[wireTypeToken] != "base64" || source["media_type"] != "image/png" || source["data"] != "AAAA" {
		t.Fatalf("base64 source = %v", source)
	}
	urlSource := content[2]["source"].(map[string]any)
	if urlSource[wireTypeToken] != "url" || urlSource["url"] != "https://example.com/cat.png" {
		t.Fatalf("url source = %v", urlSource)
	}
}
