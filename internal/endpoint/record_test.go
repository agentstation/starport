package endpoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testRecord(pid int) Record {
	return Record{
		SchemaVersion: RecordSchemaVersion, Scheme: "http", Host: "127.0.0.1", Port: 7373,
		PID: pid, StartedAt: time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC),
	}
}

func TestRecordRoundTripsForALiveProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "gateway-endpoint.json")
	want := testRecord(os.Getpid())
	require.NoError(t, WriteRecord(path, want))

	got, found, err := ReadRecord(path)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, want, got)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(recordFileMode), info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1, "the atomic write leaves no temporary file")
}

func TestReadRecordIgnoresAMissingOrExitedRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-endpoint.json")
	_, found, err := ReadRecord(path)
	require.NoError(t, err)
	require.False(t, found)

	require.NoError(t, WriteRecord(path, testRecord(exitedProcess(t))))
	_, found, err = ReadRecord(path)
	require.NoError(t, err)
	require.False(t, found, "a record of an exited process does not name a running gateway")
}

func TestReadRecordRefusesAnUnknownSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-endpoint.json")
	record := testRecord(os.Getpid())
	record.SchemaVersion = 2
	require.NoError(t, WriteRecord(path, record))
	_, found, err := ReadRecord(path)
	require.ErrorContains(t, err, "schema version 2")
	require.False(t, found)
}

func TestRemoveRecordKeepsAnotherProcessRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-endpoint.json")
	require.NoError(t, RemoveRecord(path), "a missing record needs no removal")

	require.NoError(t, WriteRecord(path, testRecord(os.Getpid()+1)))
	require.NoError(t, RemoveRecord(path))
	require.FileExists(t, path, "a later gateway owns its own record")

	require.NoError(t, WriteRecord(path, testRecord(os.Getpid())))
	require.NoError(t, RemoveRecord(path))
	require.NoFileExists(t, path)
}

// exitedProcess returns the process ID of a child that already exited.
func exitedProcess(t *testing.T) int {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	command := exec.CommandContext(t.Context(), executable, "-test.run=^$")
	require.NoError(t, command.Run())
	return command.Process.Pid
}
