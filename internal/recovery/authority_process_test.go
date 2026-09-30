package recovery

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	legacyjson "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

const authorityProcessFixture = "STARPORT_RECOVERY_AUTHORITY_PROCESS"
const authorityProcessResponse = "STARPORT_RECOVERY_AUTHORITY_RESPONSE "

type authorityProcessInput struct {
	SQL        sqlstore.Config
	KV         storage.ValkeyConfig
	Deployment string
}

type authorityProcessCommand struct {
	Key    string
	Finish bool
}

type authorityProcessReply struct {
	Ready       bool
	Approval    Record
	Reachable   bool
	Closed      bool
	Conflict    bool
	Incarnation bool
	Written     bool
	OtherError  bool
}

func runAuthorityProcess(t *testing.T, path string) {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var input authorityProcessInput
	require.NoError(t, json.Unmarshal(body, &input, legacyjson.FormatDurationAsNano(true)))
	db, err := sqlstore.Open(input.SQL)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	store, err := storage.OpenValkey(input.KV)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	witness, err := New(db)
	require.NoError(t, err)
	owner, err := witness.OpenAuthority(t.Context(), store.(storage.IncarnationProvider), input.Deployment)
	writeReply := func(reply authorityProcessReply) {
		data, err := json.Marshal(reply)
		require.NoError(t, err)
		_, err = os.Stdout.Write(append(append([]byte(authorityProcessResponse), data...), '\n'))
		require.NoError(t, err)
	}
	if err != nil {
		writeReply(authorityErrorReply(err))
		return
	}
	writeReply(authorityProcessReply{Ready: true, Approval: owner.approval})
	decoder := jsontext.NewDecoder(os.Stdin)
	for {
		var command authorityProcessCommand
		if err := json.UnmarshalDecode(decoder, &command); errors.Is(err, io.EOF) {
			return
		} else {
			require.NoError(t, err)
		}
		if command.Finish {
			writeReply(authorityProcessReply{Ready: true})
			return
		}
		err := owner.CompareAndSwapInWindow(t.Context(), []storage.CompareAndSwapMutation{{Key: command.Key, NewValue: []byte("native-permit")}}, storage.TimeWindow{})
		reply := authorityErrorReply(err)
		reply.Written = err == nil
		reply.Reachable = store.Ping(t.Context()) == nil
		writeReply(reply)
	}
}

func authorityErrorReply(err error) authorityProcessReply {
	return authorityProcessReply{Closed: errors.Is(err, ErrClosed), Conflict: errors.Is(err, ErrConflict), Incarnation: errors.Is(err, storage.ErrIncarnationChanged), OtherError: err != nil && !errors.Is(err, ErrClosed) && !errors.Is(err, ErrConflict) && !errors.Is(err, storage.ErrIncarnationChanged)}
}

type authorityChild struct {
	command *exec.Cmd
	input   io.WriteCloser
	replies chan authorityProcessReply
	done    chan struct{}
	output  bytes.Buffer
	waitErr error
	stopped bool
}

func startAuthorityChild(t *testing.T, input authorityProcessInput) (*authorityChild, authorityProcessReply) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "private")
	directory, err := productfiles.CreateDirectory(root)
	require.NoError(t, err)
	body, err := json.Marshal(input, legacyjson.FormatDurationAsNano(true))
	require.NoError(t, err)
	require.NoError(t, directory.CompareAndPublish(t.Context(), "fixture.json", nil, body))
	child := &authorityChild{command: exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRecoveryAuthorityAcrossRealProcesses$", "-test.timeout=2m"), replies: make(chan authorityProcessReply, 8), done: make(chan struct{})}
	child.command.Env = append(os.Environ(), authorityProcessFixture+"="+filepath.Join(root, "fixture.json"))
	child.command.Stderr = &child.output
	child.input, err = child.command.StdinPipe()
	require.NoError(t, err)
	output, err := child.command.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, child.command.Start())
	go func() {
		reader := bufio.NewScanner(output)
		for reader.Scan() {
			if data, ok := strings.CutPrefix(reader.Text(), authorityProcessResponse); ok {
				var reply authorityProcessReply
				if json.Unmarshal([]byte(data), &reply) == nil {
					child.replies <- reply
				}
			}
		}
		close(child.replies)
		child.waitErr = child.command.Wait()
		close(child.done)
	}()
	t.Cleanup(func() {
		if !child.stopped {
			_ = child.command.Process.Kill()
			<-child.done
		}
		_ = child.input.Close()
	})
	return child, child.reply(t)
}

func (child *authorityChild) reply(t *testing.T) authorityProcessReply {
	t.Helper()
	select {
	case reply, ok := <-child.replies:
		require.True(t, ok, "authority process returned no private response")
		return reply
	case <-t.Context().Done():
		t.Fatal("authority process response canceled")
	case <-time.After(10 * time.Second):
		t.Fatal("authority process response exceeded its bound")
	}
	return authorityProcessReply{}
}

func (child *authorityChild) exchange(t *testing.T, command authorityProcessCommand) authorityProcessReply {
	t.Helper()
	require.NoError(t, json.MarshalEncode(jsontext.NewEncoder(child.input), command))
	return child.reply(t)
}

func (child *authorityChild) fence(t *testing.T) {
	t.Helper()
	require.NoError(t, child.command.Process.Kill())
	select {
	case <-child.done:
	case <-time.After(5 * time.Second):
		t.Fatal("old writer did not exit after the OS process fence")
	}
	require.Error(t, child.waitErr)
	require.NotNil(t, child.command.ProcessState)
	require.False(t, child.command.ProcessState.Success())
	child.stopped = true
}

func (child *authorityChild) finish(t *testing.T) {
	t.Helper()
	require.True(t, child.exchange(t, authorityProcessCommand{Finish: true}).Ready)
	select {
	case <-child.done:
	case <-time.After(5 * time.Second):
		t.Fatal("fresh owner process did not finish")
	}
	require.NoError(t, child.waitErr, "%s", child.output.String())
	child.stopped = true
}

func TestRecoveryAuthorityAcrossRealProcesses(t *testing.T) {
	if path := os.Getenv(authorityProcessFixture); path != "" {
		runAuthorityProcess(t, path)
		return
	}
	postgres, originalURL := os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_VALKEY_URL")
	if postgres == "" || originalURL == "" {
		t.Skip("UNVERIFIED: real PostgreSQL and Valkey are required for process fencing")
	}
	for _, mode := range []string{"same-backend", "reachable-old-primary"} {
		t.Run(mode, func(t *testing.T) {
			replacementURL := originalURL
			if mode == "reachable-old-primary" {
				replacementURL = os.Getenv("TEST_VALKEY_REPLACEMENT_URL")
				if replacementURL == "" {
					t.Skip("UNVERIFIED: a distinct reachable Valkey process is required")
				}
			}
			sqlConfig := isolatedWitnessPostgres(t, sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: postgres}})
			db, err := sqlstore.Open(sqlConfig)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			require.NoError(t, db.Migrate(t.Context()))
			witness, err := New(db)
			require.NoError(t, err)
			deployment := "process-recovery-" + rand.Text()
			originalConfig := storage.ValkeyConfig{URL: originalURL, DeploymentID: deployment, AllowInsecure: true}
			original, err := storage.OpenValkey(originalConfig)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, original.Close()) })
			t.Cleanup(func() {
				require.NoError(t, original.BatchDelete(context.Background(), []string{authorityKey, "before-close", "during-closed", "stale-next-epoch", "fresh-next-epoch"}))
			})
			originalBackend := original.(storage.IncarnationProvider)
			originalID, err := originalBackend.ObserveIncarnation(t.Context())
			require.NoError(t, err)
			closed, err := witness.Initialize(t.Context(), deployment)
			require.NoError(t, err)
			approved, err := witness.ApproveAuthority(t.Context(), originalBackend, closed, originalID, "controlled-initial-owner", "initial-owner")
			require.NoError(t, err)
			retained, err := witness.OpenAuthority(t.Context(), originalBackend, deployment)
			require.NoError(t, err)
			child, startup := startAuthorityChild(t, authorityProcessInput{SQL: sqlConfig, KV: originalConfig, Deployment: deployment})
			require.True(t, startup.Ready)
			require.Equal(t, approved, startup.Approval)
			before := child.exchange(t, authorityProcessCommand{Key: "before-close"})
			require.True(t, before.Written)
			require.True(t, before.Reachable)
			closed, err = witness.Close(t.Context(), approved)
			require.NoError(t, err)
			blocked := child.exchange(t, authorityProcessCommand{Key: "during-closed"})
			require.True(t, blocked.Closed)
			require.False(t, blocked.Written)
			require.True(t, blocked.Reachable)
			_, err = original.Get(t.Context(), "during-closed")
			require.ErrorIs(t, err, storage.ErrNotFound)
			child.fence(t)
			current, err := witness.Current(t.Context(), deployment)
			require.NoError(t, err)
			require.Equal(t, closed, current, "OS fencing precedes reopening approval")
			replacementConfig := originalConfig
			replacementConfig.URL = replacementURL
			replacement, err := storage.OpenValkey(replacementConfig)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, replacement.Close()) })
			t.Cleanup(func() {
				require.NoError(t, replacement.BatchDelete(context.Background(), []string{authorityKey, "before-close", "during-closed", "stale-next-epoch", "fresh-next-epoch"}))
			})
			replacementBackend := replacement.(storage.IncarnationProvider)
			replacementID, err := replacementBackend.ObserveIncarnation(t.Context())
			require.NoError(t, err)
			if mode == "reachable-old-primary" {
				require.NotEqual(t, originalID, replacementID)
			}
			reopened, err := witness.ApproveAuthority(t.Context(), replacementBackend, closed, replacementID, "controlled-OS-fenced-owner", "replacement-owner")
			require.NoError(t, err)
			require.Equal(t, closed.Epoch, reopened.Epoch)
			require.Greater(t, reopened.Epoch, approved.Epoch)
			require.ErrorIs(t, retained.Check(t.Context()), ErrConflict)
			err = retained.CompareAndSwapInWindow(t.Context(), []storage.CompareAndSwapMutation{{Key: "stale-next-epoch", NewValue: []byte("must-not-appear")}}, storage.TimeWindow{})
			require.ErrorIs(t, err, ErrConflict)
			for _, store := range []storage.KVStore{original, replacement} {
				_, err := store.Get(t.Context(), "stale-next-epoch")
				require.ErrorIs(t, err, storage.ErrNotFound)
			}
			require.NoError(t, original.Ping(t.Context()), "the former primary remains reachable to the control process")
			if mode == "reachable-old-primary" {
				_, err := originalBackend.BindIncarnation(t.Context(), replacementID)
				require.ErrorIs(t, err, storage.ErrIncarnationChanged)
				refused, startup := startAuthorityChild(t, authorityProcessInput{SQL: sqlConfig, KV: originalConfig, Deployment: deployment})
				require.False(t, startup.Ready)
				require.True(t, startup.Incarnation)
				<-refused.done
				require.NoError(t, refused.waitErr)
				refused.stopped = true
			}
			fresh, startup := startAuthorityChild(t, authorityProcessInput{SQL: sqlConfig, KV: replacementConfig, Deployment: deployment})
			require.True(t, startup.Ready)
			require.Equal(t, reopened, startup.Approval)
			permit := fresh.exchange(t, authorityProcessCommand{Key: "fresh-next-epoch"})
			require.True(t, permit.Written)
			require.True(t, permit.Reachable)
			fresh.finish(t)
			actual, err := replacement.Get(t.Context(), "fresh-next-epoch")
			require.NoError(t, err)
			require.Equal(t, []byte("native-permit"), actual)
		})
	}
}
