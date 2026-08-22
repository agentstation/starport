package connectors

import "strings"

// parseImageDataURL splits a data URL ("data:image/png;base64,AAAA")
// into its media type and base64 payload. It reports false for any
// other URL shape, including remote http(s) references.
func parseImageDataURL(url string) (mediaType, data string, ok bool) {
	rest, found := strings.CutPrefix(url, "data:")
	if !found {
		return "", "", false
	}
	meta, payload, found := strings.Cut(rest, ",")
	if !found {
		return "", "", false
	}
	mediaType, found = strings.CutSuffix(meta, ";base64")
	if !found || mediaType == "" {
		return "", "", false
	}
	return mediaType, payload, true
}

// contentText joins the text parts of one message's content. Image
// parts contribute nothing; a plain string passes through unchanged.
func contentText(content MessageContent) string {
	parts, err := ParseMessageContent(content)
	if err != nil {
		return ""
	}
	var texts []string
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n\n")
}
