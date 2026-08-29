package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/server/controllers"
)

// fileRoutes are the five paths the files surface publishes, with the scope
// each one demands. Reading and writing are separate scopes because they are
// separate powers: a key that may read a stored document should not thereby be
// able to fill the deployment's storage.
var fileRoutes = []struct {
	method string
	path   string
	scope  string
}{
	{method: http.MethodPost, path: "/v1/files", scope: "files:write"},
	{method: http.MethodGet, path: "/v1/files", scope: "files:read"},
	{method: http.MethodGet, path: "/v1/files/{file_id}", scope: "files:read"},
	{method: http.MethodDelete, path: "/v1/files/{file_id}", scope: "files:write"},
	{method: http.MethodGet, path: "/v1/files/{file_id}/content", scope: "files:read"},
}

// TestServerRegistersTheFilePaths holds FIL-V10. It walks the router the
// server built rather than reading the source that builds it. A path mounted
// under the wrong group reads as present to a source scan and answers 404 to a
// caller.
func TestServerRegistersTheFilePaths(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})

	registered := map[string]bool{}
	walk := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registered[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	}
	require.NoError(t, chi.Walk(server.router, walk))

	for _, route := range fileRoutes {
		require.Truef(t, registered[route.method+" "+route.path],
			"%s %s is not registered", route.method, route.path)
	}
}

// TestFileLifecycleOverTheRoutes uploads, lists, reads, downloads, and deletes
// one file through the router. Each step is a separate route, and the value
// the previous step returned is what the next one names, so a mismatch between
// the object a caller reads and the identifier a caller may use fails here.
func TestFileLifecycleOverTheRoutes(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20, MaxFileUploadSize: 1 << 20})
	key := storeFileTestKey(t, server, "file-owner", "files:read", "files:write")

	const payload = "the quarterly report, in bytes"
	created := uploadFile(t, server, key, "report.txt", "user_data", payload)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())

	object := decodeFileObject(t, created)
	require.NotEmpty(t, object["id"])
	require.Equal(t, "file", object["object"])
	require.Equal(t, "report.txt", object["filename"])
	require.Equal(t, "user_data", object["purpose"])
	require.Equal(t, float64(len(payload)), object["bytes"])
	require.Equal(t, "processed", object["status"])
	id, _ := object["id"].(string)

	listed := doFileRequest(t, server, http.MethodGet, "/v1/files", key, nil, "")
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var envelope struct {
		Object  string           `json:"object"`
		Data    []map[string]any `json:"data"`
		FirstID string           `json:"first_id"`
		HasMore bool             `json:"has_more"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &envelope))
	require.Equal(t, "list", envelope.Object)
	require.Len(t, envelope.Data, 1)
	require.Equal(t, id, envelope.Data[0]["id"])
	require.Equal(t, id, envelope.FirstID)
	require.False(t, envelope.HasMore)

	fetched := doFileRequest(t, server, http.MethodGet, "/v1/files/"+id, key, nil, "")
	require.Equal(t, http.StatusOK, fetched.Code, fetched.Body.String())
	require.Equal(t, id, decodeFileObject(t, fetched)["id"])

	content := doFileRequest(t, server, http.MethodGet, "/v1/files/"+id+"/content", key, nil, "")
	require.Equal(t, http.StatusOK, content.Code)
	require.Equal(t, payload, content.Body.String())
	require.Contains(t, content.Header().Get("Content-Disposition"), `filename="report.txt"`)

	deleted := doFileRequest(t, server, http.MethodDelete, "/v1/files/"+id, key, nil, "")
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	require.Equal(t, true, decodeFileObject(t, deleted)["deleted"])

	// Every read of a deleted file answers not-found, including the content
	// path. A record that outlived its bytes would answer 200 with nothing.
	for _, path := range []string{"/v1/files/" + id, "/v1/files/" + id + "/content"} {
		gone := doFileRequest(t, server, http.MethodGet, path, key, nil, "")
		require.Equalf(t, http.StatusNotFound, gone.Code, "%s answered %d", path, gone.Code)
	}
}

// TestFileRoutesCarryTheirScopes holds FIL-V11. The refusal half and the
// acceptance half both matter: a scope no key can satisfy refuses every caller
// just as completely as a missing route.
func TestFileRoutesCarryTheirScopes(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20, MaxFileUploadSize: 1 << 20})
	reader := storeFileTestKey(t, server, "file-reader", "files:read")
	writer := storeFileTestKey(t, server, "file-writer", "files:read", "files:write")
	chatOnly := storeFileTestKey(t, server, "file-chat-only", "chat:write")

	// A key holding only files:read cannot upload.
	refused := uploadFile(t, server, reader, "report.txt", "user_data", "bytes")
	require.Equal(t, http.StatusForbidden, refused.Code, refused.Body.String())

	// A key holding chat access alone reaches neither side of the surface.
	require.Equal(t, http.StatusForbidden,
		doFileRequest(t, server, http.MethodGet, "/v1/files", chatOnly, nil, "").Code)

	stored := uploadFile(t, server, writer, "report.txt", "user_data", "bytes")
	require.Equal(t, http.StatusOK, stored.Code, stored.Body.String())
	id, _ := decodeFileObject(t, stored)["id"].(string)

	// The reader reads what the writer stored, and still cannot delete it.
	require.Equal(t, http.StatusOK,
		doFileRequest(t, server, http.MethodGet, "/v1/files/"+id, reader, nil, "").Code)
	require.Equal(t, http.StatusForbidden,
		doFileRequest(t, server, http.MethodDelete, "/v1/files/"+id, reader, nil, "").Code)
}

// TestUploadPastTheBoundWritesNothing holds FIL-V12. The bound is applied on
// the way in, so a refused upload never reaches the byte store. A bound
// checked after the read would leave the object it was there to prevent.
func TestUploadPastTheBoundWritesNothing(t *testing.T) {
	root := t.TempDir()
	byteStore, err := blob.NewFilesystem(root)
	require.NoError(t, err)

	server := newTestServer(t,
		&Config{MaxRequestSize: 1 << 20, MaxFileUploadSize: 1024},
		withTestBlobStore(byteStore))
	key := storeFileTestKey(t, server, "file-writer", "files:read", "files:write")

	recorder := uploadFile(t, server, key, "large.bin", "user_data", strings.Repeat("x", 8192))
	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code, recorder.Body.String())
	// The message names the bound the operator set, so a caller learns the
	// limit from the refusal instead of by bisecting upload sizes.
	require.Contains(t, recorder.Body.String(), controllers.ErrUploadTooLarge.Error())
	require.Contains(t, recorder.Body.String(), "1024")

	require.Equal(t, 0, countStoredObjects(t, root), "a refused upload left bytes behind")

	// Nothing reads either, so no record survived the refusal.
	listed := doFileRequest(t, server, http.MethodGet, "/v1/files", key, nil, "")
	require.Equal(t, http.StatusOK, listed.Code)
	require.Contains(t, listed.Body.String(), `"data":[]`)

	// The bound applies to the upload alone. A file under it still stores,
	// which proves the refusal above came from the size and not from a broken
	// route.
	accepted := uploadFile(t, server, key, "small.bin", "user_data", "a few bytes")
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
}

// TestUploadRefusesAPurposeThisGatewayDoesNotServe covers the fourth step of
// the task: a refused purpose names the set the gateway does accept, so a
// caller fixes the request from the answer alone.
func TestUploadRefusesAPurposeThisGatewayDoesNotServe(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20, MaxFileUploadSize: 1 << 20})
	key := storeFileTestKey(t, server, "file-writer", "files:read", "files:write")

	for _, purpose := range []string{"assistants", "batch", "fine-tune", ""} {
		recorder := uploadFile(t, server, key, "report.txt", purpose, "bytes")
		require.Equalf(t, http.StatusBadRequest, recorder.Code, "purpose %q was accepted", purpose)
		require.Contains(t, recorder.Body.String(), "user_data")
		require.Contains(t, recorder.Body.String(), "vision")
	}
}

// TestOneAccountCannotReadAnotherAccountsFile states the isolation rule at the
// HTTP edge. Two keys under two accounts store one file each, and neither
// identifier resolves for the other.
func TestOneAccountCannotReadAnotherAccountsFile(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20, MaxFileUploadSize: 1 << 20})
	first := storeFileTestKeyForAccount(t, server, "file-account-one", "account-one", "files:read", "files:write")
	second := storeFileTestKeyForAccount(t, server, "file-account-two", "account-two", "files:read", "files:write")

	stored := uploadFile(t, server, first, "report.txt", "user_data", "the first account's bytes")
	require.Equal(t, http.StatusOK, stored.Code, stored.Body.String())
	id, _ := decodeFileObject(t, stored)["id"].(string)

	// Not-found rather than forbidden. Any other answer would report that the
	// identifier names a real file somewhere else.
	for _, path := range []string{"/v1/files/" + id, "/v1/files/" + id + "/content"} {
		require.Equalf(t, http.StatusNotFound,
			doFileRequest(t, server, http.MethodGet, path, second, nil, "").Code,
			"%s leaked another account's file", path)
	}
	listed := doFileRequest(t, server, http.MethodGet, "/v1/files", second, nil, "")
	require.NotContains(t, listed.Body.String(), id)
}

// TestAnonymousDeploymentReachesTheFileRoutes covers the operator running with
// authentication disabled. The anonymous key has to carry both file
// scopes, or the mode that exists to make the first request work would refuse
// half the surface.
func TestAnonymousDeploymentReachesTheFileRoutes(t *testing.T) {
	config := unauthenticatedConfig()
	config.MaxRequestSize = 1 << 20
	config.MaxFileUploadSize = 1 << 20
	server := newTestServer(t, config)

	stored := uploadFile(t, server, "", "report.txt", "user_data", "bytes")
	require.Equal(t, http.StatusOK, stored.Code, stored.Body.String())
	id, _ := decodeFileObject(t, stored)["id"].(string)

	require.Equal(t, http.StatusOK,
		doFileRequest(t, server, http.MethodGet, "/v1/files/"+id, "", nil, "").Code)
	require.Equal(t, http.StatusOK,
		doFileRequest(t, server, http.MethodDelete, "/v1/files/"+id, "", nil, "").Code)
}

// TestFileUploadIsNotBoundByTheJSONRequestLimit states why the general body
// limit steps aside on the upload path. The limit exists to stop a caller from
// making the gateway hold and decode a huge document; an upload streams to a
// store, and the operator bounds it separately. Without the exemption the
// smaller of the two limits would silently win.
func TestFileUploadIsNotBoundByTheJSONRequestLimit(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 2048, MaxFileUploadSize: 1 << 20})
	key := storeFileTestKey(t, server, "file-writer", "files:read", "files:write")

	recorder := uploadFile(t, server, key, "report.txt", "user_data", strings.Repeat("x", 8192))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, float64(8192), decodeFileObject(t, recorder)["bytes"])
}

// TestUploadShortensItsRetentionWindow covers the wire half of FIL5. A
// multipart form has no nesting, so the bracket is part of the field name.
func TestUploadShortensItsRetentionWindow(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20, MaxFileUploadSize: 1 << 20})
	key := storeFileTestKey(t, server, "file-writer", "files:read", "files:write")

	recorder := uploadFileWithFields(t, server, key, "report.txt", "bytes", map[string]string{
		"purpose":                "user_data",
		"expires_after[anchor]":  "created_at",
		"expires_after[seconds]": "7200",
	})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	object := decodeFileObject(t, recorder)
	created, _ := object["created_at"].(float64)
	expires, _ := object["expires_at"].(float64)
	require.Equal(t, float64(7200), expires-created)

	// A window longer than the deployment allows is refused rather than
	// clamped, and the refusal reaches the caller as a client error.
	tooLong := uploadFileWithFields(t, server, key, "report.txt", "bytes", map[string]string{
		"purpose":                "user_data",
		"expires_after[anchor]":  "created_at",
		"expires_after[seconds]": "31536000",
	})
	require.Equal(t, http.StatusBadRequest, tooLong.Code, tooLong.Body.String())

	// An anchor this gateway does not serve would apply the window from a
	// moment the caller did not mean.
	badAnchor := uploadFileWithFields(t, server, key, "report.txt", "bytes", map[string]string{
		"purpose":                "user_data",
		"expires_after[anchor]":  "last_active_at",
		"expires_after[seconds]": "7200",
	})
	require.Equal(t, http.StatusBadRequest, badAnchor.Code, badAnchor.Body.String())
}

// TestAFullAccountRefusesAnUploadAndSaysWhy covers the wire half of FIL6. The
// bound the key carries is not the request bound: the upload is a legal size,
// and what it does not fit inside is the room the account has left. The two
// need different answers, so the refusal says which one it is.
func TestAFullAccountRefusesAnUploadAndSaysWhy(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20, MaxFileUploadSize: 1 << 20})
	bound := int64(1024)
	key := storeFileTestKeyWithLimits(t, server, "file-bounded", &limits.Limits{StoredBytes: &bound},
		"files:read", "files:write")

	payload := strings.Repeat("x", 700)
	first := uploadFile(t, server, key, "first.txt", "user_data", payload)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())

	second := uploadFile(t, server, key, "second.txt", "user_data", payload)
	require.Equal(t, http.StatusRequestEntityTooLarge, second.Code, second.Body.String())
	require.Contains(t, second.Body.String(), "Delete a file to make room")

	// The account still holds exactly the one file that fit, and deleting it
	// makes the refused upload land.
	object := decodeFileObject(t, first)
	id, _ := object["id"].(string)
	require.NotEmpty(t, id)
	deleted := doFileRequest(t, server, http.MethodDelete, "/v1/files/"+id, key, nil, "")
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())

	retried := uploadFile(t, server, key, "second.txt", "user_data", payload)
	require.Equal(t, http.StatusOK, retried.Code, retried.Body.String())
}

func storeFileTestKey(t *testing.T, server *Server, id string, scopes ...string) string {
	t.Helper()
	return storeFileTestKeyForAccount(t, server, id, "", scopes...)
}

// storeFileTestKeyWithLimits stores a key carrying its own limits.
func storeFileTestKeyWithLimits(
	t *testing.T,
	server *Server,
	id string,
	keyLimits *limits.Limits,
	scopes ...string,
) string {
	t.Helper()
	secret := "sk-starport-" + id
	hash := sha256.Sum256([]byte(secret))
	_, err := server.apiKeys.Create(t.Context(), apikey.APIKey{
		ID:        id,
		Name:      id,
		Hash:      hex.EncodeToString(hash[:]),
		Scopes:    scopes,
		Limits:    keyLimits,
		Active:    true,
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)
	return secret
}

// storeFileTestKeyForAccount issues one key under a named account. An empty
// account name leaves the key on the canonical account, which is where a
// single-account deployment puts every key.
func storeFileTestKeyForAccount(t *testing.T, server *Server, id, accountID string, scopes ...string) string {
	t.Helper()
	secret := "sk-starport-" + id
	hash := sha256.Sum256([]byte(secret))
	_, err := server.apiKeys.Create(t.Context(), apikey.APIKey{
		ID:        id,
		Name:      id,
		Hash:      hex.EncodeToString(hash[:]),
		AccountID: accountID,
		Scopes:    scopes,
		Active:    true,
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)
	return secret
}

// uploadFile posts one multipart upload. It builds the body itself rather than
// using a helper, because the part names are the wire contract this route
// serves.
func uploadFile(t *testing.T, server *Server, key, filename, purpose, payload string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write([]byte(payload))
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("purpose", purpose))
	require.NoError(t, writer.Close())
	return doFileRequest(t, server, http.MethodPost, "/v1/files", key, body.Bytes(), writer.FormDataContentType())
}

// uploadFileWithFields posts an upload carrying arbitrary form fields.
func uploadFileWithFields(
	t *testing.T,
	server *Server,
	key, filename, payload string,
	fields map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write([]byte(payload))
	require.NoError(t, err)
	for name, value := range fields {
		require.NoError(t, writer.WriteField(name, value))
	}
	require.NoError(t, writer.Close())
	return doFileRequest(t, server, http.MethodPost, "/v1/files", key, body.Bytes(), writer.FormDataContentType())
}

func doFileRequest(
	t *testing.T,
	server *Server,
	method, path, key string,
	body []byte,
	contentType string,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)
	return recorder
}

func decodeFileObject(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var object map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &object), recorder.Body.String())
	return object
}

// countStoredObjects counts the regular files the filesystem backend holds.
// Staging files count too, so a partial write that was never renamed still
// fails the assertion.
func countStoredObjects(t *testing.T, root string) int {
	t.Helper()
	var count int
	require.NoError(t, filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !entry.IsDir() {
			count++
		}
		return nil
	}))
	return count
}
