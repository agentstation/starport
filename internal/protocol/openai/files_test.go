package openai_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/protocol/openai"
)

// TestStoredFileEncodesEveryRecordedField holds FIL-V13. An SDK decodes the
// object by key, so a renamed or dropped key breaks a client that Starport
// never sees. The test names each key rather than comparing structs, because a
// struct comparison would follow a rename and report nothing.
func TestStoredFileEncodesEveryRecordedField(t *testing.T) {
	t.Parallel()

	file := openai.StoredFile{
		ID:        "file-0123456789abcdef",
		Object:    openai.StoredFileObject,
		Bytes:     4096,
		CreatedAt: 1756252800,
		Filename:  "invoice.pdf",
		Purpose:   "user_data",
		ExpiresAt: 1756339200,
		Status:    openai.StoredFileStatusProcessed,
	}

	var decoded map[string]any
	require.NoError(t, roundTrip(t, file, &decoded))

	require.Equal(t, map[string]any{
		"id":         "file-0123456789abcdef",
		"object":     "file",
		"bytes":      float64(4096),
		"created_at": float64(1756252800),
		"filename":   "invoice.pdf",
		"purpose":    "user_data",
		"expires_at": float64(1756339200),
		"status":     "processed",
	}, decoded)

	// status_details stays absent. Upstream fills it from fine-tune
	// validation, and Starport validates no fine-tune file.
	require.NotContains(t, decoded, "status_details")
}

// TestStoredFileOmitsAnUnsetExpiry keeps the optional field optional. A file
// the deployment keeps forever carries no expires_at, and a zero would tell a
// client the file expired at the unix epoch.
func TestStoredFileOmitsAnUnsetExpiry(t *testing.T) {
	t.Parallel()

	var decoded map[string]any
	require.NoError(t, roundTrip(t, openai.StoredFile{ID: "file-1", Object: openai.StoredFileObject}, &decoded))
	require.NotContains(t, decoded, "expires_at")

	// bytes and created_at stay present at zero. A client reads them on every
	// object, and an absent size reads as an unknown size.
	require.Contains(t, decoded, "bytes")
	require.Contains(t, decoded, "created_at")
}

// TestStoredFileListNamesItsEdges holds the paging contract. A client pages
// with last_id, so the envelope has to name the edge of the page the server
// actually returned rather than the edge the caller asked for.
func TestStoredFileListNamesItsEdges(t *testing.T) {
	t.Parallel()

	list := openai.NewStoredFileList([]openai.StoredFile{{ID: "file-1"}, {ID: "file-2"}}, true)
	require.Equal(t, "list", list.Object)
	require.Equal(t, "file-1", list.FirstID)
	require.Equal(t, "file-2", list.LastID)
	require.True(t, list.HasMore)
}

// TestEmptyStoredFileListEncodesAnArray holds the shape a client ranges over.
// A nil slice encodes as null, and a client that iterates data without a nil
// test fails on an account that owns no file yet.
func TestEmptyStoredFileListEncodesAnArray(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(openai.NewStoredFileList(nil, false))
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"data":[]`)
	require.Contains(t, string(encoded), `"has_more":false`)
	require.NotContains(t, string(encoded), "first_id")
}

func TestStoredFileDeletionAnswersWithTheIdentifier(t *testing.T) {
	t.Parallel()

	var decoded map[string]any
	require.NoError(t, roundTrip(t, openai.NewStoredFileDeletion("file-1"), &decoded))
	require.Equal(t, map[string]any{"id": "file-1", "object": "file", "deleted": true}, decoded)
}

func roundTrip(t *testing.T, value any, target any) error {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return json.Unmarshal(encoded, target)
}
