package catalog

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/sources"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type fleetProcessRequest struct {
	Operation, Model string
	Partial          bool
}
type fleetProcessReply struct {
	Error  string
	Head   runtime.FleetHead
	Models []string
}
type fleetProcess struct {
	command   *exec.Cmd
	input     io.WriteCloser
	replies   chan fleetProcessReply
	done      chan error
	stopped   bool
	lastLines []string
}

func startFleetProcess(t *testing.T, deployment, directory, source string) *fleetProcess {
	t.Helper()
	binary, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestFleetRuntimeProcessHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "CSP11_RUNTIME_DEPLOYMENT="+deployment, "CSP11_RUNTIME_DIRECTORY="+directory, "CSP11_RUNTIME_SOURCE="+source)
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	output, err := cmd.StdoutPipe()
	require.NoError(t, err)
	cmd.Stderr = os.Stderr
	child := &fleetProcess{command: cmd, input: input, replies: make(chan fleetProcessReply, 2), done: make(chan error, 1)}
	require.NoError(t, cmd.Start())
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "CSP11_RUNTIME=") {
				child.lastLines = append(child.lastLines, line)
				if len(child.lastLines) > 64 {
					child.lastLines = child.lastLines[1:]
				}
				continue
			}
			var reply fleetProcessReply
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "CSP11_RUNTIME=")), &reply); err != nil {
				child.replies <- fleetProcessReply{Error: err.Error()}
				continue
			}
			child.replies <- reply
		}
		child.done <- cmd.Wait()
		close(child.replies)
	}()
	t.Cleanup(func() {
		if !child.stopped {
			_ = cmd.Process.Kill()
			<-child.done
			child.stopped = true
		}
		_ = input.Close()
	})
	ready := child.read(t)
	require.Empty(t, ready.Error)
	return child
}

func (p *fleetProcess) read(t *testing.T) fleetProcessReply {
	t.Helper()
	select {
	case reply, open := <-p.replies:
		if open {
			return reply
		}
		err := <-p.done
		p.stopped = true
		t.Fatalf("catalog process ended before reply: %v\n%s", err, strings.Join(p.lastLines, "\n"))
	case <-time.After(150 * time.Second):
		t.Fatal("catalog process did not reply within startup bound")
	}
	return fleetProcessReply{}
}
func (p *fleetProcess) request(t *testing.T, request fleetProcessRequest) fleetProcessReply {
	t.Helper()
	require.NoError(t, json.NewEncoder(p.input).Encode(request))
	return p.read(t)
}
func (p *fleetProcess) close(t *testing.T) {
	t.Helper()
	require.Empty(t, p.request(t, fleetProcessRequest{Operation: "close"}).Error)
	require.NoError(t, <-p.done)
	p.stopped = true
}

func TestFleetRuntimeProcessesRecoverAfterLeaderDirectoryLoss(t *testing.T) {
	fleet, kv, _ := fleetTestStore(t)
	root := t.TempDir()
	source := filepath.Join(root, "source.json")
	baseline := testEmptyCatalog(t, "fleet-fixture")
	payload, err := catalogs.EncodeCatalogPayload(baseline)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(source, payload, 0o600))
	leaderDirectory := filepath.Join(root, "leader")
	leader := startFleetProcess(t, fleet.identity.DeploymentID, leaderDirectory, source)
	require.Empty(t, leader.request(t, fleetProcessRequest{Operation: "source"}).Error)
	first := leader.request(t, fleetProcessRequest{Operation: "publish", Model: "first"})
	require.Empty(t, first.Error)
	followerDirectory := filepath.Join(root, "follower")
	follower := startFleetProcess(t, fleet.identity.DeploymentID, followerDirectory, source)
	require.Empty(t, follower.request(t, fleetProcessRequest{Operation: "follow"}).Error)
	// Both processes have an outstanding publication request. Only the owner may commit.
	require.NoError(t, json.NewEncoder(leader.input).Encode(fleetProcessRequest{Operation: "publish", Model: "second", Partial: true}))
	require.NoError(t, json.NewEncoder(follower.input).Encode(fleetProcessRequest{Operation: "publish", Model: "not-owned", Partial: true}))
	refused := follower.read(t)
	require.NotEmpty(t, refused.Error)
	second := leader.read(t)
	require.Empty(t, second.Error)
	// No notification channel connects these processes. Explicit durable catch-up must recover the head.
	followed := follower.request(t, fleetProcessRequest{Operation: "follow"})
	require.Empty(t, followed.Error)
	require.Equal(t, second.Head, followed.Head)
	require.Equal(t, []string{"first", "second"}, followed.Models)
	follower.close(t)
	follower = startFleetProcess(t, fleet.identity.DeploymentID, followerDirectory, source)
	retained := follower.request(t, fleetProcessRequest{Operation: "follow"})
	require.Empty(t, retained.Error)
	require.Equal(t, followed.Models, retained.Models)
	require.NoError(t, leader.command.Process.Kill())
	require.Error(t, <-leader.done)
	leader.stopped = true
	require.NoError(t, os.RemoveAll(leaderDirectory))
	// Expire the dead owner's native lease without waiting its production 90-second TTL.
	require.NoError(t, kv.ExpireAt(t.Context(), fleet.prefix+"lease", time.Now().Add(-time.Second)))
	third := follower.request(t, fleetProcessRequest{Operation: "publish", Model: "third", Partial: true})
	require.Empty(t, third.Error)
	require.Equal(t, []string{"first", "second", "third"}, third.Models)
	require.Greater(t, third.Head.Revision, second.Head.Revision)
	follower.close(t)
	replacement := startFleetProcess(t, fleet.identity.DeploymentID, filepath.Join(root, "replacement"), source)
	recovered := replacement.request(t, fleetProcessRequest{Operation: "follow"})
	require.Empty(t, recovered.Error)
	require.Equal(t, third.Models, recovered.Models)
	replacement.close(t)
}

func TestFleetRuntimeProcessHelper(t *testing.T) {
	deployment := os.Getenv("CSP11_RUNTIME_DEPLOYMENT")
	if deployment == "" {
		return
	}
	kv, witness, _ := fleetTestStores(t)
	fleet, err := NewFleetStore(t.Context(), kv.(storage.IncarnationProvider), witness, deployment)
	require.NoError(t, err)
	settings := identityTestSettings(os.Getenv("CSP11_RUNTIME_DIRECTORY"), "", "127.0.0.1:0")
	settings.DeploymentID = deployment
	settings.Source = string(runtime.SourceFile)
	settings.SourceURL = os.Getenv("CSP11_RUNTIME_SOURCE")
	connected, err := openRuntime(t.Context(), kv, settings, runtimeCollectors{fleet: fleet})
	require.NoError(t, err)
	defer func() { require.NoError(t, connected.Close(context.Background())) }()
	send := func(reply fleetProcessReply) {
		data, err := json.Marshal(reply)
		require.NoError(t, err)
		fmt.Println("CSP11_RUNTIME=" + string(data))
	}
	send(fleetProcessReply{})
	decoder := json.NewDecoder(os.Stdin)
	for {
		var request fleetProcessRequest
		if err := decoder.Decode(&request); err != nil {
			require.ErrorIs(t, err, io.EOF)
			return
		}
		var workErr error
		switch request.Operation {
		case "close":
			send(fleetProcessReply{})
			return
		case "source":
			_, workErr = connected.runtime.RefreshSource(t.Context())
		case "follow":
			workErr = connected.RefreshFleet(t.Context())
		case "publish":
			_, workErr = connected.runtime.PublishObservations(t.Context(), fleetProcessObservation(t, request.Model, request.Partial))
		default:
			t.Fatalf("unknown process operation %q", request.Operation)
		}
		reply := fleetProcessReply{}
		if workErr == nil {
			candidate, err := connected.CurrentCandidate(t.Context())
			workErr = err
			if workErr == nil {
				workErr = connected.Accept(t.Context(), candidate)
				reply.Head = candidate.FleetHead
				provider, err := candidate.State.Catalog.Provider("fleet-fixture")
				require.NoError(t, err)
				for id := range provider.Models {
					reply.Models = append(reply.Models, id)
				}
				slices.Sort(reply.Models)
			}
		}
		if workErr != nil {
			reply.Error = workErr.Error()
		}
		send(reply)
	}
}

func fleetProcessObservation(t *testing.T, model string, partial bool) sources.Observation {
	t.Helper()
	builder := catalogs.NewEmpty()
	require.NoError(t, builder.SetAuthor(catalogs.Author{ID: "fixture", Name: "Fixture"}))
	require.NoError(t, builder.SetAuthorModel("fixture", catalogs.Model{ID: model, Name: model, Authors: []catalogs.Author{{ID: "fixture", Name: "Fixture"}}}))
	require.NoError(t, builder.SetProvider(catalogs.Provider{ID: "fleet-fixture", Name: "Fleet fixture", Models: map[string]*catalogs.Model{model: {ID: model, Name: model, ModelRef: catalogs.ModelDefinitionID("fixture/" + model)}}}))
	catalog, err := builder.Build()
	require.NoError(t, err)
	metadata := sources.ObservationMetadata{ObservedAt: time.Now().UTC(), Revision: sources.Revision{Kind: sources.RevisionKindContentDigest}, Completeness: sources.ObservationCompletenessComplete, Status: sources.ObservationStatusSucceeded, Records: sources.ObservationRecordCounts{Accepted: 1}}
	if partial {
		metadata.Completeness = sources.ObservationCompletenessPartial
		metadata.Status = sources.ObservationStatusDegraded
		metadata.Records.Rejected = 1
		metadata.Issues = []sources.ObservationIssue{{Scope: sources.ObservationIssueScopeRecord, Code: sources.ObservationIssueCodeInvalidRecord, Subject: "invalid", Message: "fixture rejection"}}
	}
	observation, err := sources.NewObservation(sources.ReleaseArtifactID, catalog, metadata)
	require.NoError(t, err)
	return observation
}
