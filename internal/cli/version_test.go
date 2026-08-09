package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWriteVersionText(t *testing.T) {
	info := versionFixture()
	var output bytes.Buffer
	if err := WriteVersion(&output, info, false); err != nil {
		t.Fatalf("write text version: %v", err)
	}
	for _, want := range []string{
		"Starport v1.2.3",
		"Build time: 2026-08-09T00:00:00Z",
		"Git commit: abc123",
		"Git branch: main",
		"Go version: go1.26.5",
		"OS/Arch: testos/testarch",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("text output does not contain %q:\n%s", want, output.String())
		}
	}
}

func TestWriteVersionJSON(t *testing.T) {
	info := versionFixture()
	var output bytes.Buffer
	if err := WriteVersion(&output, info, true); err != nil {
		t.Fatalf("write JSON version: %v", err)
	}
	var got BuildInfo
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON version: %v", err)
	}
	if got != info {
		t.Errorf("JSON version = %#v, want %#v", got, info)
	}
}

func versionFixture() BuildInfo {
	return BuildInfo{
		Version: "v1.2.3", BuildTime: "2026-08-09T00:00:00Z",
		GitCommit: "abc123", GitBranch: "main", GoVersion: "go1.26.5",
		OS: "testos", Arch: "testarch",
	}
}
