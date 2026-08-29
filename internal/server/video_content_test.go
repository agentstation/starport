package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
)

// videoAssetBytes stands for a finished video. The route serves whatever the
// byte store holds, so the content is arbitrary and the identity of it is not.
var videoAssetBytes = []byte("starport-stored-video-bytes")

// storedVideoJob writes one completed job whose asset is already in the byte
// store, and answers the key the object sits under.
//
// It writes the record and the object directly rather than running a provider
// through the whole submit and poll path. This file is about what the content
// route answers for a job in that state, and reaching the state through a fake
// provider would put the fake between the test and the rule.
func storedVideoJob(
	t *testing.T,
	records jobs.Repository,
	byteStore blob.Store,
	account string,
	expiresAt time.Time,
) jobs.Job {
	t.Helper()
	now := time.Now()
	job, err := jobs.New("job-with-stored-content", account, "mock", "mock/video-1",
		routing.OperationVideosGenerations, now)
	require.NoError(t, err)
	require.NoError(t, job.AdoptProviderJob("provider-side-identifier"))
	require.NoError(t, job.Transition(jobs.JobStateRunning, now))
	require.NoError(t, job.Transition(jobs.JobStateCompleted, now))

	key := "storedvideoassetkey"
	info, err := byteStore.Put(t.Context(), key, bytes.NewReader(videoAssetBytes))
	require.NoError(t, err)
	require.NoError(t, job.StoreAsset(key, "video/mp4", info.Size, expiresAt))
	require.NoError(t, records.Create(t.Context(), job))
	return job
}

// TestVideoContentServesStarportBytes states the rule that gives a Starport job
// identifier its worth. A provider serves a finished video from a link that
// expires and that carries the provider's own credential. A caller holding a
// Starport identifier therefore reads the bytes from Starport, and never a
// redirect it cannot follow to a link this gateway cannot promise.
func TestVideoContentServesStarportBytes(t *testing.T) {
	store := storage.NewMockStore()
	byteStore, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20},
		withTestStore(store), withTestBlobStore(byteStore))

	records, err := jobs.OpenRepository(store)
	require.NoError(t, err)
	job := storedVideoJob(t, records, byteStore, "acme", time.Now().Add(time.Hour))
	require.NotEmpty(t, job.AssetKey)

	key := storeFileTestKeyForAccount(t, server, "video-content-owner", "acme", "videos:write")
	recorder := videoRequest(server, http.MethodGet, "/v1/videos/"+job.ID+"/content", key)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, "video/mp4", recorder.Header().Get("Content-Type"))
	require.Equal(t, strconv.Itoa(len(videoAssetBytes)), recorder.Header().Get("Content-Length"))
	require.Equal(t, videoAssetBytes, recorder.Body.Bytes())

	// Never a redirect. A 302 to a provider link is the shape this route exists
	// to refuse, and it would pass a test that only asserted the caller
	// eventually got bytes.
	require.NotEqual(t, http.StatusFound, recorder.Code)
	require.Empty(t, recorder.Header().Get("Location"))

	// The Starport storage key is not the thing a caller reads, and a body or a
	// header that named it would let a caller address the byte store directly.
	require.NotContains(t, recorder.Body.String(), job.AssetKey)
	require.NotContains(t, recorder.Body.String(), "provider-side-identifier")
}

// TestAnExpiredVideoAssetAnswersGone separates the two answers about missing
// bytes. A job that never produced an asset reads not found. A job that
// produced one this gateway no longer keeps reads gone, and the body states the
// window, because that is the fact a caller needs to collect the next one in
// time.
func TestAnExpiredVideoAssetAnswersGone(t *testing.T) {
	store := storage.NewMockStore()
	byteStore, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20},
		withTestStore(store), withTestBlobStore(byteStore))

	records, err := jobs.OpenRepository(store)
	require.NoError(t, err)
	job := storedVideoJob(t, records, byteStore, "acme", time.Now().Add(-time.Minute))

	key := storeFileTestKeyForAccount(t, server, "video-expired-owner", "acme", "videos:write")
	recorder := videoRequest(server, http.MethodGet, "/v1/videos/"+job.ID+"/content", key)

	require.Equal(t, http.StatusGone, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "24h")

	// The read marked the record and reclaimed the bytes. Refusing to serve an
	// expired asset while keeping it would make the window a statement about
	// the route rather than about the storage.
	expired, err := records.Get(t.Context(), "acme", job.ID)
	require.NoError(t, err)
	require.False(t, expired.AssetExpiredAt.IsZero())
	require.Equal(t, jobs.JobStateCompleted, expired.State)
	_, err = byteStore.Stat(t.Context(), job.AssetKey)
	require.ErrorIs(t, err, blob.ErrNotFound)

	// The job itself still reads. A caller that comes back late learns its work
	// completed, which is a different fact from a job that never ran.
	listed := videoRequest(server, http.MethodGet, "/v1/videos", key)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	require.Contains(t, listed.Body.String(), job.ID)
}

// TestAListedJobStatesWhetherItsBytesAreStillThere puts the retention window
// on the answer a caller already reads.
//
// A reader with a page of finished jobs has to tell the ones it can still play
// from the ones it can only read about. Without the window on the listing the
// only way to ask is to fetch each asset and read the refusal, which spends a
// request per row to learn the page is unplayable. The window is also the fact
// an OpenAI client already expects on this object.
//
// The window travels only while there are bytes behind it. The record keeps
// AssetExpiresAt after the sweep takes the asset, so reporting it then would
// point a caller at a video that is already gone.
func TestAListedJobStatesWhetherItsBytesAreStillThere(t *testing.T) {
	store := storage.NewMockStore()
	byteStore, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20},
		withTestStore(store), withTestBlobStore(byteStore))

	records, err := jobs.OpenRepository(store)
	require.NoError(t, err)
	window := time.Now().Add(time.Hour).Truncate(time.Second)
	job := storedVideoJob(t, records, byteStore, "acme", window)

	key := storeFileTestKeyForAccount(t, server, "video-window-owner", "acme", "videos:write")

	held := decodeVideoJob(t, videoRequest(server, http.MethodGet, "/v1/videos/"+job.ID, key))
	require.Equal(t, "completed", held["status"])
	require.Equal(t, float64(window.Unix()), held["expires_at"])

	// Take the bytes the way the sweep does. The record stays and the window
	// stops travelling with it.
	stored, err := records.Get(t.Context(), "acme", job.ID)
	require.NoError(t, err)
	require.NoError(t, stored.ExpireAsset(time.Now()))
	require.NoError(t, records.Replace(t.Context(), stored))

	gone := decodeVideoJob(t, videoRequest(server, http.MethodGet, "/v1/videos/"+job.ID, key))
	require.Equal(t, "completed", gone["status"],
		"the work happened, so the state does not move when the bytes go")
	require.NotContains(t, gone, "expires_at")
}

// decodeVideoJob reads one job object off a recorded answer.
func decodeVideoJob(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var wire map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &wire))
	return wire
}

// TestVideoContentOfAJobWithNoAssetIsNotFound covers the third state. A running
// job, a failed job, and a completed job whose fetch has not landed all hold no
// bytes, and none of them expired.
func TestVideoContentOfAJobWithNoAssetIsNotFound(t *testing.T) {
	store := storage.NewMockStore()
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withTestStore(store))

	records, err := jobs.OpenRepository(store)
	require.NoError(t, err)
	job, err := jobs.New("job-without-content", "acme", "mock", "mock/video-1",
		routing.OperationVideosGenerations, time.Now())
	require.NoError(t, err)
	require.NoError(t, job.AdoptProviderJob("provider-side-identifier"))
	require.NoError(t, records.Create(t.Context(), job))

	key := storeFileTestKeyForAccount(t, server, "video-pending-owner", "acme", "videos:write")
	recorder := videoRequest(server, http.MethodGet, "/v1/videos/"+job.ID+"/content", key)
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
}
