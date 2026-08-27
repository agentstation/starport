package routing

import "sort"

// Operation is one provider inference operation selected from catalog facts.
// The values match Starmap's provider operation names exactly, because the
// catalog is the only source that names an operation.
type Operation string

const (
	// OperationChatCompletions generates chat completions.
	OperationChatCompletions Operation = "chat-completions"
	// OperationEmbeddings generates vector embeddings.
	OperationEmbeddings Operation = "embeddings"
	// OperationImagesGenerations generates an image from a prompt.
	OperationImagesGenerations Operation = "images-generations"
	// OperationImagesEdits generates an image from a prompt and a source image.
	OperationImagesEdits Operation = "images-edits"
	// OperationAudioSpeech generates speech from text.
	OperationAudioSpeech Operation = "audio-speech"
	// OperationAudioTranscriptions writes recorded speech as text in its own
	// language.
	OperationAudioTranscriptions Operation = "audio-transcriptions"
	// OperationAudioTranslations writes recorded speech as English text.
	OperationAudioTranslations Operation = "audio-translations"
)

// OperationSet is an immutable set of operation names. One set answers the
// three separate questions the gateway asks about an operation: whether a
// caller may request it, whether a catalog fact that names it can reach a
// route, and whether a compiled transport may declare it. Three answers from
// one set is what keeps a widened catalog from disagreeing with a narrower
// build.
type OperationSet struct {
	members map[Operation]struct{}
}

// NewOperationSet builds a set from the named operations. It ignores the empty
// name, which means "the caller stated no operation" rather than an operation.
func NewOperationSet(operations ...Operation) OperationSet {
	members := make(map[Operation]struct{}, len(operations))
	for _, operation := range operations {
		if operation == "" {
			continue
		}
		members[operation] = struct{}{}
	}
	return OperationSet{members: members}
}

// Contains reports whether the set names the operation.
func (s OperationSet) Contains(operation Operation) bool {
	if s.members == nil {
		return false
	}
	_, exists := s.members[operation]
	return exists
}

// Len returns how many operations the set names.
func (s OperationSet) Len() int {
	return len(s.members)
}

// Members returns the names in sorted order, so an error message built from a
// set reads the same on every run.
func (s OperationSet) Members() []Operation {
	result := make([]Operation, 0, len(s.members))
	for operation := range s.members {
		result = append(result, operation)
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result
}

// servedOperations names every operation this build can plan. A catalog that
// names an operation outside the set describes a gateway that has not shipped
// yet, so the planner treats the fact as inert rather than as corruption.
var servedOperations = NewOperationSet(
	OperationChatCompletions,
	OperationEmbeddings,
	OperationImagesGenerations,
	OperationImagesEdits,
	OperationAudioSpeech,
	OperationAudioTranscriptions,
	OperationAudioTranslations,
)

// ServedOperations returns the operations this build can plan.
func ServedOperations() OperationSet {
	return servedOperations
}
