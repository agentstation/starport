package endpoint

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// RecordSchemaVersion is the version of the endpoint record format.
const RecordSchemaVersion = 1

const (
	recordFileMode      = 0o600
	recordDirectoryMode = 0o700
)

// Record states where a running gateway listens. The gateway that binds
// writes it under the state root and removes it on close, so a sibling
// command such as starport ui reaches that gateway and not the configured
// port. It holds no credential.
type Record struct {
	SchemaVersion int       `json:"schema_version"`
	Scheme        string    `json:"scheme"`
	Host          string    `json:"host"`
	Port          int       `json:"port"`
	PID           int       `json:"pid"`
	StartedAt     time.Time `json:"started_at"`
}

// WriteRecord replaces the record at path atomically at mode 0600.
func WriteRecord(path string, record Record) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode the endpoint record: %w", err)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, recordDirectoryMode); err != nil {
		return fmt.Errorf("create the endpoint record directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".gateway-endpoint-*")
	if err != nil {
		return fmt.Errorf("create a temporary endpoint record: %w", err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if err := temporary.Chmod(recordFileMode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("restrict the temporary endpoint record: %w", err)
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write the temporary endpoint record: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close the temporary endpoint record: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace the endpoint record: %w", err)
	}
	return nil
}

// ReadRecord returns the record at path when the process that wrote it is
// alive. A missing record, or a record whose process exited, reports false.
func ReadRecord(path string) (Record, bool, error) {
	record, err := readRecord(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	if !alive(record.PID) {
		return Record{}, false, nil
	}
	return record, true, nil
}

// RemoveRecord removes the record at path when this process wrote it. A
// later gateway can replace the record, and its record stays.
func RemoveRecord(path string) error {
	record, err := readRecord(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.PID != os.Getpid() {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the endpoint record: %w", err)
	}
	return nil
}

func readRecord(path string) (Record, error) {
	// #nosec G304 -- the path is the configured state root's endpoint record.
	encoded, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(encoded, &record); err != nil {
		return Record{}, fmt.Errorf("decode the endpoint record: %w", err)
	}
	if record.SchemaVersion != RecordSchemaVersion {
		return Record{}, fmt.Errorf("endpoint record schema version %d is not supported", record.SchemaVersion)
	}
	return record, nil
}
