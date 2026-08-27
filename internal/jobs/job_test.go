// The tests here sit outside the package on purpose. A job holds one value a
// caller must never reach, and a test that can see the field cannot prove that
// a caller cannot.
package jobs_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/routing"
)

var submitted = time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

func newTestJob(t *testing.T) jobs.Job {
	t.Helper()
	job, err := jobs.New(
		"job_01",
		"tenant_a",
		"black-forest-labs/FLUX-1-dev",
		routing.OperationImagesGenerations,
		submitted,
	)
	require.NoError(t, err)
	return job
}

// TestTheTransitionTableAcceptsOnlyTheLegalPairs walks all twenty-five pairs.
// The expectation is written out rather than derived, so a table edit that
// widens the graph has to be made twice before it lands.
func TestTheTransitionTableAcceptsOnlyTheLegalPairs(t *testing.T) {
	t.Parallel()

	legal := map[jobs.JobState]map[jobs.JobState]bool{
		jobs.JobStateQueued: {
			jobs.JobStateRunning:   true,
			jobs.JobStateCompleted: true,
			jobs.JobStateFailed:    true,
			jobs.JobStateCancelled: true,
		},
		jobs.JobStateRunning: {
			jobs.JobStateCompleted: true,
			jobs.JobStateFailed:    true,
			jobs.JobStateCancelled: true,
		},
		jobs.JobStateCompleted: {},
		jobs.JobStateFailed:    {},
		jobs.JobStateCancelled: {},
	}

	states := jobs.JobStates()
	require.Len(t, states, 5, "the state set changed without this test")
	for _, from := range states {
		require.Contains(t, legal, from, "state %q is missing an expectation", from)
		for _, to := range states {
			require.Equalf(
				t,
				legal[from][to],
				jobs.CanTransition(from, to),
				"%s to %s", from, to,
			)
		}
	}
}

// TestAStateNeverTransitionsToItself holds the rule a repeated poll depends
// on. A provider that answers the same word twice reports no change, and a
// table that accepted the pair would stamp a new terminal time each time.
func TestAStateNeverTransitionsToItself(t *testing.T) {
	t.Parallel()

	for _, state := range jobs.JobStates() {
		require.Falsef(t, jobs.CanTransition(state, state), "%s to itself", state)
	}
}

// TestAnUnknownStateWordTransitionsNowhere keeps a provider word that nothing
// mapped from entering the graph in either direction.
func TestAnUnknownStateWordTransitionsNowhere(t *testing.T) {
	t.Parallel()

	unknown := jobs.JobState("expired")
	require.False(t, unknown.Valid())
	require.False(t, unknown.Terminal())
	for _, state := range jobs.JobStates() {
		require.Falsef(t, jobs.CanTransition(unknown, state), "expired to %s", state)
		require.Falsef(t, jobs.CanTransition(state, unknown), "%s to expired", state)
	}
}

// TestNoTerminalStateAcceptsATransition is the rule accounting and retention
// both rest on. A job that left a terminal state would draw a second usage
// record and restart its retention window.
func TestNoTerminalStateAcceptsATransition(t *testing.T) {
	t.Parallel()

	ended := submitted.Add(time.Minute)
	later := ended.Add(time.Hour)

	for _, terminal := range []jobs.JobState{
		jobs.JobStateCompleted,
		jobs.JobStateFailed,
		jobs.JobStateCancelled,
	} {
		require.Truef(t, terminal.Terminal(), "%s is not terminal", terminal)

		for _, to := range jobs.JobStates() {
			require.Falsef(t, jobs.CanTransition(terminal, to), "%s to %s", terminal, to)
		}

		job := newTestJob(t)
		require.NoError(t, job.Transition(terminal, ended))
		require.Equal(t, ended, job.TerminalAt)

		err := job.Transition(jobs.JobStateRunning, later)
		require.ErrorIs(t, err, jobs.ErrIllegalTransition)
		require.Equal(t, terminal, job.State, "the refused move changed the state")
		require.Equal(t, ended, job.TerminalAt, "the refused move restamped the end")
	}

	require.False(t, jobs.JobStateQueued.Terminal())
	require.False(t, jobs.JobStateRunning.Terminal())
}

// TestATerminalMoveStampsItsEndOnce holds the pairing of state and time. A
// terminal state with no end time cannot start a retention window, and a
// running state with one would end a window that never opened.
func TestATerminalMoveStampsItsEndOnce(t *testing.T) {
	t.Parallel()

	started := submitted.Add(time.Second)
	ended := submitted.Add(time.Minute)

	job := newTestJob(t)
	require.Equal(t, jobs.JobStateQueued, job.State)
	require.True(t, job.TerminalAt.IsZero())
	require.NoError(t, job.Validate())

	require.NoError(t, job.Transition(jobs.JobStateRunning, started))
	require.True(t, job.TerminalAt.IsZero(), "a running job recorded an end")
	require.NoError(t, job.Validate())

	require.NoError(t, job.Transition(jobs.JobStateCompleted, ended))
	require.Equal(t, ended, job.TerminalAt)
	require.NoError(t, job.Validate())
}

func TestValidateRefusesARecordAStoreCannotAnswerWith(t *testing.T) {
	t.Parallel()

	valid := newTestJob(t)
	require.NoError(t, valid.Validate())

	tests := map[string]func(job *jobs.Job){
		"no identifier": func(job *jobs.Job) { job.ID = " " },
		"no tenant":     func(job *jobs.Job) { job.Tenant = "" },
		"no model":      func(job *jobs.Job) { job.Model = "" },
		"no operation":  func(job *jobs.Job) { job.Operation = "" },
		"no created at": func(job *jobs.Job) { job.CreatedAt = time.Time{} },
		"unknown state": func(job *jobs.Job) { job.State = "expired" },
		"terminal state with no end": func(job *jobs.Job) {
			job.State = jobs.JobStateCompleted
		},
		"running state with an end": func(job *jobs.Job) {
			job.TerminalAt = submitted.Add(time.Minute)
		},
	}
	for name, breakIt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			job := newTestJob(t)
			breakIt(&job)
			require.ErrorIs(t, job.Validate(), jobs.ErrInvalidJob)
		})
	}

	_, err := jobs.New("", "tenant_a", "m", routing.OperationImagesGenerations, submitted)
	require.ErrorIs(t, err, jobs.ErrInvalidJob)
}

// TestTheRecordHandsOutNoProviderJobIdentifier is the reason this test file
// sits outside the package. A caller that learned the provider identifier
// could poll the provider directly, outside every limit Starport applies and
// every usage record it keeps.
func TestTheRecordHandsOutNoProviderJobIdentifier(t *testing.T) {
	t.Parallel()

	const providerJobID = "video_4f19c0a7"

	job := newTestJob(t)
	require.False(t, job.HasProviderJob())
	require.NoError(t, job.AdoptProviderJob(providerJobID))
	require.True(t, job.HasProviderJob())

	recordType := reflect.TypeOf(job)
	field, held := recordType.FieldByName("providerJobID")
	require.True(t, held, "the record no longer holds the identifier privately")
	require.NotEmpty(t, field.PkgPath, "providerJobID became an exported field")

	record := reflect.ValueOf(job)
	for index := range recordType.NumField() {
		exported := recordType.Field(index)
		if exported.PkgPath != "" {
			continue
		}
		require.NotContainsf(
			t,
			fmt.Sprint(record.Field(index).Interface()),
			providerJobID,
			"exported field %s carries the provider job identifier", exported.Name,
		)
	}

	for index := range recordType.NumMethod() {
		method := recordType.Method(index)
		if method.Type.NumIn() != 1 {
			continue
		}
		for _, answer := range method.Func.Call([]reflect.Value{record}) {
			require.NotContainsf(
				t,
				fmt.Sprint(answer.Interface()),
				providerJobID,
				"method %s answers with the provider job identifier", method.Name,
			)
		}
	}

	// A struct with an unexported field still prints it under %v, so the two
	// ways a record most often reaches a reader get their own assertions.
	require.NotContains(t, fmt.Sprintf("%v", job), providerJobID, "a log line carries it")
	require.NotContains(t, fmt.Sprintf("%+v", job), providerJobID, "a log line carries it")

	encoded, err := json.Marshal(job)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), providerJobID, "a response body carries it")
}

func TestAdoptProviderJobRefusesAnEmptyAnswer(t *testing.T) {
	t.Parallel()

	job := newTestJob(t)
	require.ErrorIs(t, job.AdoptProviderJob("  "), jobs.ErrInvalidJob)
	require.False(t, job.HasProviderJob())
}
