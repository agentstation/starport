package storage

import (
	"testing"
)

func TestSerialize(t *testing.T) {
	tests := []struct {
		name    string
		input   any
		wantErr bool
	}{
		{
			name:    "string",
			input:   "test string",
			wantErr: false,
		},
		{
			name:    "int",
			input:   42,
			wantErr: false,
		},
		{
			name:    "struct",
			input:   struct{ Name string }{Name: "test"},
			wantErr: false,
		},
		{
			name:    "map",
			input:   map[string]any{"key": "value"},
			wantErr: false,
		},
		{
			name:    "slice",
			input:   []int{1, 2, 3},
			wantErr: false,
		},
		{
			name:    "nil",
			input:   nil,
			wantErr: false,
		},
		{
			name:    "channel",
			input:   make(chan int),
			wantErr: true, // channels cannot be serialized
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := Serialize(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Serialize() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && data == nil {
				t.Error("Serialize() returned nil data without error")
			}
		})
	}
}

func TestDeserialize(t *testing.T) {
	type testStruct struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	tests := []struct {
		name    string
		data    []byte
		target  any
		wantErr bool
		check   func(t *testing.T, v any)
	}{
		{
			name:    "string",
			data:    []byte(`"test string"`),
			target:  new(string),
			wantErr: false,
			check: func(t *testing.T, v any) {
				if got := *v.(*string); got != "test string" {
					t.Errorf("Expected 'test string', got %s", got)
				}
			},
		},
		{
			name:    "int",
			data:    []byte(`42`),
			target:  new(int),
			wantErr: false,
			check: func(t *testing.T, v any) {
				if got := *v.(*int); got != 42 {
					t.Errorf("Expected 42, got %d", got)
				}
			},
		},
		{
			name:    "struct",
			data:    []byte(`{"name":"test","value":123}`),
			target:  new(testStruct),
			wantErr: false,
			check: func(t *testing.T, v any) {
				got := v.(*testStruct)
				if got.Name != "test" || got.Value != 123 {
					t.Errorf("Expected {test 123}, got %+v", got)
				}
			},
		},
		{
			name:    "invalid json",
			data:    []byte(`{invalid`),
			target:  new(string),
			wantErr: true,
		},
		{
			name:    "type mismatch",
			data:    []byte(`"string"`),
			target:  new(int),
			wantErr: true,
		},
		{
			name:    "nil data",
			data:    nil,
			target:  new(string),
			wantErr: true,
		},
		{
			name:    "empty data",
			data:    []byte{},
			target:  new(string),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Deserialize(tt.data, tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("Deserialize() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.check != nil {
				tt.check(t, tt.target)
			}
		})
	}
}

func TestSerializeDeserializeRoundTrip(t *testing.T) {
	type complexStruct struct {
		ID       string         `json:"id"`
		Name     string         `json:"name"`
		Age      int            `json:"age"`
		Tags     []string       `json:"tags"`
		Metadata map[string]any `json:"metadata"`
	}

	original := complexStruct{
		ID:   "123",
		Name: "Test User",
		Age:  30,
		Tags: []string{"tag1", "tag2", "tag3"},
		Metadata: map[string]any{
			"key1": "value1",
			"key2": 42,
			"key3": true,
		},
	}

	// Serialize
	data, err := Serialize(original)
	if err != nil {
		t.Fatalf("Serialize failed: %v", err)
	}

	// Deserialize
	var result complexStruct
	err = Deserialize(data, &result)
	if err != nil {
		t.Fatalf("Deserialize failed: %v", err)
	}

	// Compare
	if result.ID != original.ID {
		t.Errorf("ID mismatch: got %s, want %s", result.ID, original.ID)
	}
	if result.Name != original.Name {
		t.Errorf("Name mismatch: got %s, want %s", result.Name, original.Name)
	}
	if result.Age != original.Age {
		t.Errorf("Age mismatch: got %d, want %d", result.Age, original.Age)
	}
	if len(result.Tags) != len(original.Tags) {
		t.Errorf("Tags length mismatch: got %d, want %d", len(result.Tags), len(original.Tags))
	}
	for i := range original.Tags {
		if result.Tags[i] != original.Tags[i] {
			t.Errorf("Tag[%d] mismatch: got %s, want %s", i, result.Tags[i], original.Tags[i])
		}
	}
}

func TestSerializeString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []byte
	}{
		{
			name:  "empty string",
			input: "",
			want:  []byte{},
		},
		{
			name:  "simple string",
			input: "hello",
			want:  []byte("hello"),
		},
		{
			name:  "unicode string",
			input: "Hello 世界",
			want:  []byte("Hello 世界"),
		},
		{
			name:  "special characters",
			input: "line1\nline2\ttab",
			want:  []byte("line1\nline2\ttab"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SerializeString(tt.input)
			if string(got) != string(tt.want) {
				t.Errorf("SerializeString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeserializeString(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{
			name:  "empty bytes",
			input: []byte{},
			want:  "",
		},
		{
			name:  "simple string",
			input: []byte("hello"),
			want:  "hello",
		},
		{
			name:  "unicode string",
			input: []byte("Hello 世界"),
			want:  "Hello 世界",
		},
		{
			name:  "nil bytes",
			input: nil,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeserializeString(tt.input)
			if got != tt.want {
				t.Errorf("DeserializeString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSerializeInt64(t *testing.T) {
	tests := []struct {
		name  string
		input int64
		want  string
	}{
		{
			name:  "zero",
			input: 0,
			want:  "0",
		},
		{
			name:  "positive",
			input: 123456789,
			want:  "123456789",
		},
		{
			name:  "negative",
			input: -123456789,
			want:  "-123456789",
		},
		{
			name:  "max int64",
			input: 9223372036854775807,
			want:  "9223372036854775807",
		},
		{
			name:  "min int64",
			input: -9223372036854775808,
			want:  "-9223372036854775808",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SerializeInt64(tt.input)
			if string(got) != tt.want {
				t.Errorf("SerializeInt64() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDeserializeInt64(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		want    int64
		wantErr bool
	}{
		{
			name:    "zero as string",
			input:   []byte("0"),
			want:    0,
			wantErr: false,
		},
		{
			name:    "zero as json number",
			input:   []byte("0"),
			want:    0,
			wantErr: false,
		},
		{
			name:    "positive",
			input:   []byte("123456789"),
			want:    123456789,
			wantErr: false,
		},
		{
			name:    "negative",
			input:   []byte("-123456789"),
			want:    -123456789,
			wantErr: false,
		},
		{
			name:    "invalid format",
			input:   []byte("not-a-number"),
			want:    0,
			wantErr: true,
		},
		{
			name:    "empty",
			input:   []byte(""),
			want:    0,
			wantErr: true,
		},
		{
			name:    "nil",
			input:   nil,
			want:    0,
			wantErr: true,
		},
		{
			name:    "float",
			input:   []byte("123.456"),
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DeserializeInt64(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("DeserializeInt64() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("DeserializeInt64() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInt64RoundTrip(t *testing.T) {
	values := []int64{0, 1, -1, 1000, -1000, 9223372036854775807, -9223372036854775808}

	for _, original := range values {
		data := SerializeInt64(original)
		result, err := DeserializeInt64(data)
		if err != nil {
			t.Errorf("DeserializeInt64 failed for %d: %v", original, err)
			continue
		}
		if result != original {
			t.Errorf("Round trip failed: got %d, want %d", result, original)
		}
	}
}
