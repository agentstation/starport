package privatefiles

import (
	"encoding/json/v2"
	"os"
	"testing"
)

// This test-only overlay uses the pinned producer's private publication owner.
func TestStarportActivationPublicationProbe(t *testing.T) {
	fixturePath := os.Getenv("STARPORT_PUBLICATION_PROBE_FIXTURE")
	if fixturePath == "" {
		t.Fatal("private fixture is required")
	}
	body, err := os.ReadFile(fixturePath)
	if err != nil || len(body) > 1<<20 {
		t.Fatal("invalid private fixture")
	}
	var fixture struct {
		Directory string
		Name      string
		Data      []byte
		Crash     string
	}
	if json.Unmarshal(body, &fixture) != nil || len(fixture.Data) > 1<<20 {
		t.Fatal("invalid private fixture record")
	}
	directoryPath := fixture.Directory
	{
		d, err := ExistingDirectory(directoryPath)
		if err != nil {
			t.Fatal(err)
		}
		writer, err := d.acquirePublicationWriter(t.Context(), true)
		if err != nil {
			t.Fatal(err)
		}
		phase := fixture.Crash
		name, prefix := fixture.Name, ".product-stage-"
		if phase == "foreign" {
			name = "other.json"
		}
		if phase == "wrong-prefix" {
			prefix = ".other-"
		}
		journal, err := writer.newJournal(name, prefix)
		if err != nil {
			t.Fatal(err)
		}
		if phase == "header" {
			os.Exit(88)
		}
		file, err := CreateFile(writer.root, journal.header.Stage)
		if err != nil {
			t.Fatal(err)
		}
		if phase == "unowned" {
			os.Exit(88)
		}
		empty, err := publicationRecordOf(writer.root, journal.header.Stage, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := journal.append(writer, publicationEvent{Record: &empty}); err != nil {
			t.Fatal(err)
		}
		if phase == "empty" {
			os.Exit(88)
		}
		data := fixture.Data
		if phase == "wrong-bytes" {
			data = []byte("different-native-phase")
		}
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := file.Sync(); err != nil {
			t.Fatal(err)
		}
		if phase == "incomplete" {
			os.Exit(88)
		}
		written, err := publicationRecordOf(writer.root, journal.header.Stage, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if err := journal.append(writer, publicationEvent{Record: &written}); err != nil {
			t.Fatal(err)
		}
		if phase == "published" {
			if _, err := d.promotePublication(t.Context(), writer, journal, name, nil, written); err != nil {
				t.Fatal(err)
			}
		}
		os.Exit(88)
	}
}
