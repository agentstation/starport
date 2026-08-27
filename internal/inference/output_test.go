package inference

import (
	"fmt"
	"reflect"
	"testing"
)

// Clone is what keeps one retry attempt from rewriting the request the next
// attempt is about to send, and what keeps a cached stream replay from
// handing two readers the same bytes. Every Clone method here is written by
// hand, one line per field that owns memory. A field added without its line
// compiles, passes every existing test, and aliases in production under
// retry or replay alone.
//
// These tests do not name fields. They fill a value by reflection, clone it,
// and walk both sides looking for shared memory, so a field added tomorrow is
// covered the moment it is declared.

// fillValue gives every field distinct non-zero data. A zero field cannot
// prove independence: a nil pointer aliases nothing.
func fillValue(t *testing.T, value reflect.Value, seed *int) {
	t.Helper()
	*seed++
	switch value.Kind() {
	case reflect.String:
		value.SetString(fmt.Sprintf("value-%d", *seed))
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(int64(*seed))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value.SetUint(uint64(*seed))
	case reflect.Float32, reflect.Float64:
		value.SetFloat(float64(*seed))
	case reflect.Pointer:
		value.Set(reflect.New(value.Type().Elem()))
		fillValue(t, value.Elem(), seed)
	case reflect.Slice:
		value.Set(reflect.MakeSlice(value.Type(), 1, 1))
		fillValue(t, value.Index(0), seed)
	case reflect.Map:
		value.Set(reflect.MakeMap(value.Type()))
		key := reflect.New(value.Type().Key()).Elem()
		fillValue(t, key, seed)
		element := reflect.New(value.Type().Elem()).Elem()
		fillValue(t, element, seed)
		value.SetMapIndex(key, element)
	case reflect.Struct:
		for i := range value.NumField() {
			if !value.Field(i).CanSet() {
				continue
			}
			fillValue(t, value.Field(i), seed)
		}
	default:
		// A kind with no arm would be filled with a zero value, and a zero
		// value proves nothing. Fail rather than pass on an empty check.
		t.Fatalf("fillValue has no arm for kind %s; extend it", value.Kind())
	}
}

// assertNoSharedMemory reports every pointer, slice, and map the clone shares
// with its source. It reports all of them rather than stopping at the first,
// so one run names every missing clone line.
func assertNoSharedMemory(t *testing.T, path string, source, clone reflect.Value) {
	t.Helper()
	switch source.Kind() {
	case reflect.Pointer:
		if source.IsNil() || clone.IsNil() {
			return
		}
		if source.Pointer() == clone.Pointer() {
			t.Errorf("%s: the clone shares this pointer, so writing through one changes the other", path)
			return
		}
		assertNoSharedMemory(t, path+".*", source.Elem(), clone.Elem())
	case reflect.Slice:
		if source.Len() == 0 || clone.Len() == 0 {
			return
		}
		if source.Pointer() == clone.Pointer() {
			t.Errorf("%s: the clone shares this backing array", path)
			return
		}
		for i := range min(source.Len(), clone.Len()) {
			assertNoSharedMemory(t, fmt.Sprintf("%s[%d]", path, i), source.Index(i), clone.Index(i))
		}
	case reflect.Map:
		if source.IsNil() || clone.IsNil() {
			return
		}
		if source.Pointer() == clone.Pointer() {
			t.Errorf("%s: the clone shares this map, so a new key reaches both", path)
			return
		}
		for _, key := range source.MapKeys() {
			cloned := clone.MapIndex(key)
			if !cloned.IsValid() {
				t.Errorf("%s: the clone dropped key %v", path, key)
				continue
			}
			assertNoSharedMemory(t, fmt.Sprintf("%s[%v]", path, key), source.MapIndex(key), cloned)
		}
	case reflect.Struct:
		for i := range source.NumField() {
			assertNoSharedMemory(t, path+"."+source.Type().Field(i).Name, source.Field(i), clone.Field(i))
		}
	}
}

// cloneIndependence is one Clone method under test.
type cloneIndependence struct {
	name  string
	build func(t *testing.T) (source, clone any)
}

// TestCloneSharesNoMemory covers every canonical type that a retry or a cached
// replay clones. A media field is what makes this urgent: a request now
// carries audio bytes and a stream delta now carries audio bytes, and bytes
// are the payload a second reader would corrupt.
func TestCloneSharesNoMemory(t *testing.T) {
	t.Parallel()

	cases := []cloneIndependence{
		{"ChatRequest", func(t *testing.T) (any, any) {
			var request ChatRequest
			seed := 0
			fillValue(t, reflect.ValueOf(&request).Elem(), &seed)
			return request, request.Clone()
		}},
		{"ChatResponse", func(t *testing.T) (any, any) {
			var response ChatResponse
			seed := 0
			fillValue(t, reflect.ValueOf(&response).Elem(), &seed)
			return response, response.Clone()
		}},
		{"StreamEvent", func(t *testing.T) (any, any) {
			var event StreamEvent
			seed := 0
			fillValue(t, reflect.ValueOf(&event).Elem(), &seed)
			return event, event.Clone()
		}},
		{"EmbeddingRequest", func(t *testing.T) (any, any) {
			var request EmbeddingRequest
			seed := 0
			fillValue(t, reflect.ValueOf(&request).Elem(), &seed)
			return request, request.Clone()
		}},
		{"EmbeddingResponse", func(t *testing.T) (any, any) {
			var response EmbeddingResponse
			seed := 0
			fillValue(t, reflect.ValueOf(&response).Elem(), &seed)
			return response, response.Clone()
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			source, clone := testCase.build(t)
			assertNoSharedMemory(t, testCase.name,
				reflect.ValueOf(source), reflect.ValueOf(clone))
		})
	}
}

// TestChatResponseCloneKeepsAnImagePartIndependent states the case in the
// terms a reader recognizes, beside the reflection sweep above. A retry
// resends the assistant turn, and an aliased image would let the first
// attempt's bytes change under the second.
func TestChatResponseCloneKeepsAnImagePartIndependent(t *testing.T) {
	t.Parallel()

	response := ChatResponse{Choices: []Choice{{
		Message: Message{
			Role: RoleAssistant,
			Content: []ContentPart{{
				Kind:  ContentImage,
				Image: &Image{URL: "https://example.test/first.png", Detail: "high"},
			}},
		},
	}}}

	clone := response.Clone()
	clone.Choices[0].Message.Content[0].Image.URL = "https://example.test/second.png"

	if got := response.Choices[0].Message.Content[0].Image.URL; got != "https://example.test/first.png" {
		t.Errorf("the source image URL became %q; the clone aliases it", got)
	}
}

// TestStreamEventCloneKeepsDeltaAudioIndependent holds the field this task
// adds. ChoiceDelta carried no pointer before it, so the clone loop copied
// each delta as a struct and every field came along by value. Audio breaks
// that assumption, and its bytes are the payload a replayed stream reuses.
func TestStreamEventCloneKeepsDeltaAudioIndependent(t *testing.T) {
	t.Parallel()

	event := StreamEvent{Deltas: []ChoiceDelta{{
		Role:  RoleAssistant,
		Audio: &AudioChunk{Data: []byte{1, 2, 3}, Transcript: "hello"},
	}}}

	clone := event.Clone()
	clone.Deltas[0].Audio.Data[0] = 9
	clone.Deltas[0].Audio.Transcript = "goodbye"

	if event.Deltas[0].Audio.Data[0] != 1 {
		t.Error("the source audio bytes changed; the clone shares the chunk's slice")
	}
	if event.Deltas[0].Audio.Transcript != "hello" {
		t.Error("the source transcript changed; the clone shares the chunk")
	}
}

// TestChatRequestCloneKeepsOutputModalitiesIndependent holds the other two
// fields this task adds.
func TestChatRequestCloneKeepsOutputModalitiesIndependent(t *testing.T) {
	t.Parallel()

	request := ChatRequest{
		OutputModalities: []Modality{ModalityText, ModalityAudio},
		AudioOutput:      &AudioOutput{Voice: "alloy", Format: "wav"},
	}

	clone := request.Clone()
	clone.OutputModalities[0] = ModalityImage
	clone.AudioOutput.Voice = "verse"

	if request.OutputModalities[0] != ModalityText {
		t.Error("the source output modalities changed; the clone shares the slice")
	}
	if request.AudioOutput.Voice != "alloy" {
		t.Error("the source audio output changed; the clone shares the pointer")
	}
}
