package recovery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"math"
	"strings"
	"unicode"

	"github.com/agentstation/starport/internal/sqlstore"
)

// EpochEvidence records the operator's independent epoch evidence.
// Its digest identifies external bytes. Starport cannot prove their completeness.
type EpochEvidence struct {
	HighestEpoch int64  `json:"highest_epoch"`
	SourceSHA256 string `json:"source_sha256"`
	Reference    string `json:"reference"`
	Operator     string `json:"operator"`
}

// ImportedEpochRequest binds a closed restored record to an exact import and evidence.
type ImportedEpochRequest struct {
	Prepared Record                            `json:"prepared"`
	Snapshot sqlstore.SQLiteSnapshot           `json:"snapshot"`
	Import   sqlstore.RelationalImportIdentity `json:"import"`
	Evidence EpochEvidence                     `json:"evidence"`
}

func (r ImportedEpochRequest) next() (Record, string, error) {
	if r.Prepared.Open || r.Prepared.Epoch <= 1 || r.Prepared.Epoch == math.MaxInt64 || r.Prepared.BackendID != "" ||
		validDeployment(r.Prepared.DeploymentID) != nil ||
		r.Evidence.HighestEpoch < r.Prepared.Epoch-1 || r.Evidence.HighestEpoch >= math.MaxInt64-1 {
		return Record{}, "", ErrConflict
	}
	for _, value := range []string{r.Prepared.Evidence, r.Evidence.Reference, r.Evidence.Operator} {
		if strings.TrimSpace(value) == "" || len(value) > 4096 || strings.ContainsFunc(value, unicode.IsControl) {
			return Record{}, "", ErrConflict
		}
	}
	decoded, err := hex.DecodeString(r.Evidence.SourceSHA256)
	if err != nil || len(decoded) != sha256.Size || strings.ToLower(r.Evidence.SourceSHA256) != r.Evidence.SourceSHA256 {
		return Record{}, "", ErrConflict
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		return Record{}, "", err
	}
	digest := sha256.Sum256(encoded)
	identity := hex.EncodeToString(digest[:])
	next := r.Prepared
	next.Epoch = max(r.Prepared.Epoch, r.Evidence.HighestEpoch+1)
	next.Evidence = "restore-epoch:" + identity
	return next, identity, nil
}

// PrepareImportedEpoch advances restored authority above independently retained history.
// It retains closed admission, the import barrier, and an immutable evidence receipt.
// The operator must establish external completeness and keep every writer fenced.
func (w *Witness) PrepareImportedEpoch(ctx context.Context, request ImportedEpochRequest) (Record, error) {
	if w == nil || w.db == nil || ctx == nil {
		return Record{}, ErrClosed
	}
	next, digest, err := request.next()
	if err != nil {
		return Record{}, err
	}
	return w.reconcileEpoch(ctx, request.Snapshot, request.Import, "recovery-epoch:"+request.Prepared.DeploymentID, digest, request.Prepared, next)
}

// reconcileEpoch replaces the closed record under one native relational receipt and confirms the result.
// An exact retry reuses the receipt and does not change the record again.
func (w *Witness) reconcileEpoch(ctx context.Context, snapshot sqlstore.SQLiteSnapshot, identity sqlstore.RelationalImportIdentity, step, digest string, expected, next Record) (Record, error) {
	err := w.db.ReconcileRelationalImport(ctx, snapshot, identity, step, digest, func(ctx context.Context, conn *sql.Conn) error {
		_, err := w.replaceWith(ctx, conn, expected, next)
		return err
	})
	if err != nil {
		return Record{}, err
	}
	current, err := w.Current(ctx, next.DeploymentID)
	if err != nil || current != next {
		return Record{}, errors.Join(ErrConflict, err)
	}
	return next, nil
}
