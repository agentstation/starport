package inference

// MediaUnits counts the non-text payloads one request carries. Tokens do not
// describe media: a caller sends a number of images, seconds of audio, or
// pages of a document, and a provider prices those units separately. The
// counts therefore stay beside the token total rather than inside it.
type MediaUnits struct {
	// Images, Audio, Documents, and Videos count the parts of each kind.
	Images    int
	Audio     int
	Documents int
	Videos    int

	// InlineBytes totals the decoded bytes the request carries itself. A
	// part that names a remote reference adds nothing, because the gateway
	// never fetched the bytes behind it.
	InlineBytes int64
}

// Total counts every media unit, whatever its kind.
func (u MediaUnits) Total() int {
	return u.Images + u.Audio + u.Documents + u.Videos
}

// EstimateMediaUnits counts the media payloads of one message list. It is the
// single walk over content parts that both the token estimator and the
// accounting path read, so one new content kind is counted in one place
// rather than once per caller.
//
// A part counts by the payload it carries rather than by the kind it names.
// Older call sites build a part with a payload and no kind, and a media part
// the gateway forwards is a media part whether or not the kind was set.
func EstimateMediaUnits(messages []Message) MediaUnits {
	var units MediaUnits
	for _, message := range messages {
		for _, part := range message.Content {
			if part.Image != nil && part.Image.URL != "" {
				units.Images++
			}
			if part.Audio != nil {
				units.Audio++
				units.InlineBytes += int64(len(part.Audio.Data))
			}
			if part.Document != nil {
				units.Documents++
				units.InlineBytes += int64(len(part.Document.Data))
			}
			if part.Video != nil {
				units.Videos++
				units.InlineBytes += int64(len(part.Video.Data))
			}
		}
	}
	return units
}

// RequestMediaModalities lists the media modalities one message list carries,
// in the order ContentKinds declares them. Routing compares this list against
// the input modalities the catalog states for a model.
//
// Text is not in the list. Every chat request carries text and every
// chat-capable model reads it, so a text entry would only put a modality
// check in front of traffic that already works. The defect this list closes
// is media sent to a model that reads none.
func RequestMediaModalities(messages []Message) []Modality {
	units := EstimateMediaUnits(messages)
	if units.Total() == 0 {
		return nil
	}
	modalities := make([]Modality, 0, 4)
	if units.Images > 0 {
		modalities = append(modalities, ModalityImage)
	}
	if units.Audio > 0 {
		modalities = append(modalities, ModalityAudio)
	}
	if units.Documents > 0 {
		modalities = append(modalities, ModalityDocument)
	}
	if units.Videos > 0 {
		modalities = append(modalities, ModalityVideo)
	}
	return modalities
}
