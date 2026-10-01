package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"math"
	"strings"
	"unicode"

	"github.com/agentstation/starport/internal/sqlstore"
)

// ClosedAdoptionEpochRequest binds a closed populated record to its prior approval, exact claim, and evidence.
// The closed record keeps the backend identity of the prior approval. No imported empty witness is simulated.
type ClosedAdoptionEpochRequest struct {
	Closed        Record                            `json:"closed"`
	PriorApproval Record                            `json:"prior_approval"`
	Snapshot      sqlstore.SQLiteSnapshot           `json:"snapshot"`
	Import        sqlstore.RelationalImportIdentity `json:"import"`
	Evidence      EpochEvidence                     `json:"evidence"`
}

func (r ClosedAdoptionEpochRequest) next() (Record, string, error) {
	closed, prior := r.Closed, r.PriorApproval
	if closed.Open || closed.Epoch <= 1 || closed.Epoch == math.MaxInt64 || validDeployment(closed.DeploymentID) != nil ||
		strings.TrimSpace(closed.BackendID) == "" || len(closed.BackendID) > 256 ||
		!prior.Open || prior.Epoch <= 0 || prior.DeploymentID != closed.DeploymentID || prior.BackendID != closed.BackendID ||
		strings.TrimSpace(prior.Evidence) == "" || len(prior.Evidence) > 4096 || r.Evidence.HighestEpoch >= math.MaxInt64-1 {
		return Record{}, "", ErrConflict
	}
	if prior.Epoch >= closed.Epoch || r.Evidence.HighestEpoch < closed.Epoch-1 {
		return Record{}, "", ErrEpochConflict
	}
	for _, value := range []string{closed.Evidence, r.Evidence.Reference, r.Evidence.Operator} {
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
	next := closed
	next.Epoch = max(closed.Epoch, r.Evidence.HighestEpoch+1)
	next.Evidence = "adoption-epoch:" + identity
	return next, identity, nil
}

// PrepareClosedAdoptionEpoch advances a closed populated record above independently retained history.
// It keeps the old backend identity, closed admission, the import barrier, and an immutable evidence receipt.
// The operator must establish external completeness and keep every writer fenced.
func (w *Witness) PrepareClosedAdoptionEpoch(ctx context.Context, request ClosedAdoptionEpochRequest) (Record, error) {
	if w == nil || w.db == nil || ctx == nil {
		return Record{}, ErrClosed
	}
	next, digest, err := request.next()
	if err != nil {
		return Record{}, err
	}
	return w.reconcileEpoch(ctx, request.Snapshot, request.Import, "adoption-epoch:"+request.Closed.DeploymentID, digest, request.Closed, next)
}
