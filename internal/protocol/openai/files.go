package openai

// The file object is the wire shape of one stored upload. The codec owns the
// field names and the two envelope literals, so a controller stays HTTP
// mechanics and no other package spells a wire key.
//
// The codec holds no Starport type here on purpose. A stored file belongs to
// internal/files, and a protocol package that reached it would let the wire
// format decide what a stored file is.

const (
	// StoredFileObject is the literal every file object carries.
	StoredFileObject = "file"
	// ListObject is the literal every list envelope carries. A stored file
	// page, an embedding set, a video job page, and a rerank answer all state it.
	ListObject = "list"
)

// The status field. Upstream marks it deprecated and Starport still serves it,
// because a strict SDK decode reads it and Starport holds a real two-state
// record behind it. A record whose bytes have not finished landing reads as
// uploaded, and a readable one reads as processed.
const (
	// StoredFileStatusUploaded names a file whose bytes have not committed.
	StoredFileStatusUploaded = "uploaded"
	// StoredFileStatusProcessed names a readable file.
	StoredFileStatusProcessed = "processed"
)

// StoredFile is one file object on the wire.
//
// The status_details field is absent. Upstream fills it from fine-tune
// validation, Starport validates no fine-tune file, and a field carrying an
// invented value is worse than an absent one.
type StoredFile struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Status    string `json:"status"`
}

// StoredFileList is the list envelope.
//
// HasMore has no omitempty. A client reads the field to decide whether to page
// again, and an absent false would read the same as an absent envelope.
type StoredFileList struct {
	Object  string       `json:"object"`
	Data    []StoredFile `json:"data"`
	FirstID string       `json:"first_id,omitempty"`
	LastID  string       `json:"last_id,omitempty"`
	HasMore bool         `json:"has_more"`
}

// NewStoredFileList wraps one page and names its edges.
//
// The cursor fields come from the page rather than from the caller, so a
// client that pages with last_id reads the same order the server returned.
func NewStoredFileList(page []StoredFile, hasMore bool) StoredFileList {
	list := StoredFileList{Object: ListObject, Data: page, HasMore: hasMore}
	if list.Data == nil {
		// An empty page encodes as [] rather than null. A client that ranges
		// over data should not have to test for a missing array first.
		list.Data = []StoredFile{}
	}
	if len(page) > 0 {
		list.FirstID = page[0].ID
		list.LastID = page[len(page)-1].ID
	}
	return list
}

// StoredFileDeletion is the answer to a delete.
type StoredFileDeletion struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

// NewStoredFileDeletion names one deleted file.
func NewStoredFileDeletion(id string) StoredFileDeletion {
	return StoredFileDeletion{ID: id, Object: StoredFileObject, Deleted: true}
}
