package inference

// A batch outlives the request that starts it, the way a video generation
// does, so it carries a request shape and an answer shape. It also carries a
// line vocabulary, because the work itself arrives as a stored JSONL file
// whose every line is one request and leaves as a stored JSONL file whose
// every line is one result.
//
// Neither answer shape names a provider. Every line routes through the same
// planner an online request uses, so one batch may touch many providers, and
// the per-line answer is where a provider would appear if a caller needed it.

// BatchCreateRequest is one canonical request to run a batch.
type BatchCreateRequest struct {
	// InputFileID names the stored JSONL file the batch reads.
	InputFileID string
	// Endpoint is the one operation path every line in the batch calls, such
	// as "/v1/chat/completions".
	Endpoint string
	// CompletionWindow is the window the caller asked for. The gateway
	// validates and echoes it rather than scheduling by it, because a
	// self-hosted gateway starts the work at once.
	CompletionWindow string
}

// Clone returns a copy that shares nothing with the original.
func (r BatchCreateRequest) Clone() BatchCreateRequest { return r }

// Batch is the canonical answer a caller reads about one batch it submitted.
type Batch struct {
	// ID is the Starport batch identifier.
	ID string
	// Endpoint is the operation path every line calls.
	Endpoint string
	// InputFileID names the stored input file.
	InputFileID string
	// OutputFileID names the stored result file, or is empty while the batch
	// has not written one.
	OutputFileID string
	// ErrorFileID names the stored error file, or is empty when every line
	// succeeded or none has failed yet.
	ErrorFileID string
	// State is the canonical job state word.
	State string
	// Reason states why a failed or cancelled batch stopped. It is empty for
	// every other state.
	Reason string
	// TotalLines is how many request lines the input file holds, or zero
	// while the batch has not counted them.
	TotalLines int
	// CompletedLines is how many lines produced a result.
	CompletedLines int
	// FailedLines is how many lines produced an error-file entry.
	FailedLines int
	// CreatedUnix is when Starport recorded the batch.
	CreatedUnix int64
	// CompletedUnix is when the batch reached a terminal state, or zero while
	// it has not.
	CompletedUnix int64
}

// Clone returns a copy that shares nothing with the original.
func (b Batch) Clone() Batch { return b }

// BatchLine is one decoded request line from a batch input file.
type BatchLine struct {
	// CustomID is the caller's own name for the line. Every result carries it
	// back, because line order is the only other way to match a result to a
	// request.
	CustomID string
	// URL is the operation path the line calls. The codec has already checked
	// it against the batch endpoint.
	URL string
	// Body is the raw request body the line carries. The line runner decodes
	// it with the same codec the online route uses.
	Body []byte
}

// Clone returns a copy that shares nothing with the original.
func (l BatchLine) Clone() BatchLine {
	copied := l
	if l.Body != nil {
		copied.Body = append([]byte(nil), l.Body...)
	}
	return copied
}

// BatchLineResult is what one line produced: a response body and the status
// it would have carried on the online route. A non-2xx status sends the line
// to the error file rather than the output file.
type BatchLineResult struct {
	// CustomID is the caller's name for the line, copied from the request.
	CustomID string
	// StatusCode is the HTTP status the online route would have answered.
	StatusCode int
	// RequestID is the per-line request identifier, for support and for the
	// usage record that carries the same value.
	RequestID string
	// Body is the response body, or the error body for a failed line.
	Body []byte
}

// Clone returns a copy that shares nothing with the original.
func (r BatchLineResult) Clone() BatchLineResult {
	copied := r
	if r.Body != nil {
		copied.Body = append([]byte(nil), r.Body...)
	}
	return copied
}
