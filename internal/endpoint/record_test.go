package endpoint

import (
	"net"
	"os"
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

// holdRecord takes the seat beside the record at path for the test, as a
// running gateway would.
func holdRecord(t *testing.T, path string) {
	t.Helper()
	lock, err := LockRecord(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Close() })
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

func TestRecordRoundTripsForARunningGateway(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "gateway-endpoint.json")
	holdRecord(t, path)
	want := listeningRecord(t)
	require.NoError(t, WriteRecord(path, want))

	got, found, err := ReadRecord(path)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, want, got)
	if runtime.GOOS != "windows" {
		for _, name := range []string{path, LockPath(path)} {
			info, err := os.Stat(name)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(recordFileMode), info.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 2, "the atomic write leaves no temporary file beside the record and its lock")
}

func TestReadRecordIgnoresAMissingRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-endpoint.json")
	_, found, err := ReadRecord(path)
	require.NoError(t, err)
	require.False(t, found)
}

// TestReadRecordIgnoresARecordWhoseGatewayExited covers a record that a
// crashed gateway left behind. The operating system released its lock, and
// can have given its process ID to an unrelated process that listens on the
// recorded port. This process stands in for that unrelated process.
func TestReadRecordIgnoresARecordWhoseGatewayExited(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-endpoint.json")
	require.NoError(t, WriteRecord(path, listeningRecord(t)))
	_, found, err := ReadRecord(path)
	require.NoError(t, err)
	require.False(t, found, "a live process ID and an answering port without the lock do not name a running gateway")
}

// TestReadRecordIgnoresAGatewayThatDoesNotListen covers a gateway that holds
// the seat and no longer answers at the recorded address.
func TestReadRecordIgnoresAGatewayThatDoesNotListen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-endpoint.json")
	holdRecord(t, path)
	record := testRecord(os.Getpid())
	record.Port = closedPort(t)
	require.NoError(t, WriteRecord(path, record))
	_, found, err := ReadRecord(path)
	require.NoError(t, err)
	require.False(t, found, "a held seat without a listener does not name a running gateway")
}

// TestReadRecordReachesAGatewayOnEveryAddress dials loopback for a gateway
// that binds the unspecified address.
func TestReadRecordReachesAGatewayOnEveryAddress(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "0.0.0.0:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	path := filepath.Join(t.TempDir(), "gateway-endpoint.json")
	holdRecord(t, path)
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

// TestLockRecordSeatsOneGateway refuses a second gateway while the first holds
// the seat and admits it after the first closes.
func TestLockRecordSeatsOneGateway(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "gateway-endpoint.json")
	first, err := LockRecord(path)
	require.NoError(t, err)
	require.True(t, held(path))

	_, err = LockRecord(path)
	require.ErrorIs(t, err, ErrRecordHeld)

	require.NoError(t, first.Close())
	require.NoError(t, first.Close(), "a second close is harmless")
	require.False(t, held(path), "a probe leaves the free seat free")
	second, err := LockRecord(path)
	require.NoError(t, err)
	require.NoError(t, second.Close())
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
