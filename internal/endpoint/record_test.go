package endpoint

import (
	"net"
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

// listeningRecord returns a record of this process at a loopback port that
// the test holds, as a running gateway would.
func listeningRecord(t *testing.T) Record {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	record := testRecord(os.Getpid())
	record.Port = Port(listener.Addr())
	return record
}

// closedPort returns a loopback port that nothing listens on.
func closedPort(t *testing.T) int {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := Port(listener.Addr())
	require.NoError(t, listener.Close())
	return port
}

func TestRecordRoundTripsForALiveProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "gateway-endpoint.json")
	want := listeningRecord(t)
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

// TestReadRecordIgnoresALiveProcessThatDoesNotListen covers a recorded
// process ID that the operating system gave to an unrelated process after the
// gateway exited. The process is alive, and nothing answers at the address.
func TestReadRecordIgnoresALiveProcessThatDoesNotListen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-endpoint.json")
	record := testRecord(os.Getpid())
	record.Port = closedPort(t)
	require.NoError(t, WriteRecord(path, record))
	_, found, err := ReadRecord(path)
	require.NoError(t, err)
	require.False(t, found, "a live process ID without a listener does not name a running gateway")
}

// TestReadRecordReachesAGatewayOnEveryAddress dials loopback for a gateway
// that binds the unspecified address.
func TestReadRecordReachesAGatewayOnEveryAddress(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "0.0.0.0:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	path := filepath.Join(t.TempDir(), "gateway-endpoint.json")
	record := testRecord(os.Getpid())
	record.Host, record.Port = "0.0.0.0", Port(listener.Addr())
	require.NoError(t, WriteRecord(path, record))
	got, found, err := ReadRecord(path)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, record, got)
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
