package catalog

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/agentstation/starmap/pkg/catalogs"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/runtime"

	"github.com/agentstation/starport/internal/catalog/recovery"
	"github.com/agentstation/starport/internal/storage"
)

// FleetStore binds shared catalog publication to an independent recovery approval.
// It uses native backend expiry and retains complete acquisition inputs outside local directories.
type FleetStore struct {
	store    storage.IncarnationStore
	witness  *recovery.Witness
	approval recovery.Record
	identity runtime.FleetIdentity
	prefix   string
	session  string
}

// NewFleetStore requires an existing open recovery approval and a matching live backend.
// It never approves a missing record or a replacement backend during startup.
func NewFleetStore(ctx context.Context, store storage.IncarnationProvider, witness *recovery.Witness, deployment string) (*FleetStore, error) {
	if store == nil || witness == nil {
		return nil, errors.New("catalog fleet storage and independent recovery witness are required")
	}
	approved, err := witness.Approved(ctx, deployment)
	if err != nil {
		return nil, err
	}
	if approved.Epoch <= 0 {
		return nil, errors.New("catalog recovery epoch must be positive")
	}
	bound, err := store.BindIncarnation(ctx, approved.BackendID)
	if err != nil {
		return nil, err
	}
	return &FleetStore{
		store: bound, witness: witness, approval: approved, session: rand.Text(),
		identity: runtime.FleetIdentity{DeploymentID: deployment, RecoveryEpoch: uint64(approved.Epoch), BackendID: approved.BackendID},
		prefix:   "catalog:fleet:{" + payloadDigest([]byte(deployment)) + "}:v1:",
	}, nil
}

func (s *FleetStore) checkApproval(ctx context.Context) error {
	current, err := s.witness.Approved(ctx, s.identity.DeploymentID)
	if err != nil {
		return err
	}
	if current != s.approval {
		return fleetStoreConflict("the independent recovery approval changed")
	}
	return nil
}

// CurrentHead reads small metadata without downloading acquisition inputs.
func (s *FleetStore) CurrentHead(ctx context.Context) (runtime.FleetHead, error) {
	if err := s.checkApproval(ctx); err != nil {
		return runtime.FleetHead{}, err
	}
	data, _, err := s.store.ReadWithLifetime(ctx, s.prefix+"head", 4096)
	if err != nil {
		return runtime.FleetHead{}, fleetReadError(err, "current")
	}
	var head runtime.FleetHead
	if err := json.Unmarshal(data, &head); err != nil {
		return head, err
	}
	if err := head.Validate(); err != nil {
		return head, err
	}
	if head.Identity != s.identity {
		return head, fleetStoreConflict("the catalog head belongs to another recovery identity")
	}
	return head, nil
}

// CurrentPublication reads the immutable snapshot selected by the current head.
func (s *FleetStore) CurrentPublication(ctx context.Context) (runtime.FleetSnapshot, error) {
	head, err := s.CurrentHead(ctx)
	if err != nil {
		return runtime.FleetSnapshot{}, err
	}
	return s.selectedPublication(ctx, head)
}

func (s *FleetStore) selectedPublication(ctx context.Context, head runtime.FleetHead) (runtime.FleetSnapshot, error) {
	snapshot, err := s.Publication(ctx, head)
	if errors.Is(err, starmaperrors.ErrNotFound) {
		return runtime.FleetSnapshot{}, fmt.Errorf("selected fleet publication is unavailable: %v", err)
	}
	return snapshot, err
}

// Publication reads a retained snapshot by its complete head identity.
func (s *FleetStore) Publication(ctx context.Context, head runtime.FleetHead) (runtime.FleetSnapshot, error) {
	if err := s.checkApproval(ctx); err != nil {
		return runtime.FleetSnapshot{}, err
	}
	if err := head.Validate(); err != nil {
		return runtime.FleetSnapshot{}, err
	}
	if head.Identity != s.identity {
		return runtime.FleetSnapshot{}, fleetStoreConflict("the requested publication belongs to another recovery identity")
	}
	data, err := s.readBlob(ctx, s.publicationKey(head))
	if err != nil {
		return runtime.FleetSnapshot{}, err
	}
	var snapshot runtime.FleetSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return snapshot, err
	}
	if err := snapshot.Validate(); err != nil {
		return snapshot, err
	}
	if snapshot.Head != head {
		return snapshot, fleetStoreConflict("the stored snapshot differs from the selected head")
	}
	return snapshot, nil
}

// Get reads an immutable catalog from a committed publication.
func (s *FleetStore) Get(ctx context.Context, id string) (catalogs.Generation, error) {
	if err := s.checkApproval(ctx); err != nil {
		return catalogs.Generation{}, err
	}
	data, _, err := s.store.ReadWithLifetime(ctx, s.generationKey(id), 4096)
	if err != nil {
		return catalogs.Generation{}, fleetReadError(err, id)
	}
	var head runtime.FleetHead
	if err := json.Unmarshal(data, &head); err != nil {
		return catalogs.Generation{}, err
	}
	snapshot, err := s.Publication(ctx, head)
	if err != nil {
		return catalogs.Generation{}, err
	}
	if snapshot.Publication.Generation.Manifest.GenerationID != id {
		return catalogs.Generation{}, fleetStoreConflict("the generation index selects a different catalog")
	}
	return snapshot.Publication.Generation, nil
}

// CommitPublication checks the original live grant and predecessor in one native transaction.
func (s *FleetStore) CommitPublication(ctx context.Context, publication runtime.FleetPublication) (runtime.FleetHead, error) {
	if err := publication.Validate(); err != nil {
		return runtime.FleetHead{}, err
	}
	if err := s.checkApproval(ctx); err != nil {
		return runtime.FleetHead{}, err
	}
	if publication.Grant.Identity != s.identity {
		return runtime.FleetHead{}, fleetStoreConflict("the publication grant belongs to another recovery identity")
	}
	head := runtime.FleetHead{Identity: s.identity, Revision: publication.Expected.Revision + 1,
		GenerationID: publication.Generation.Manifest.GenerationID, RecoveryChecksum: publication.Recovery.Checksum}
	snapshot := runtime.FleetSnapshot{Head: head, Publication: publication}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return runtime.FleetHead{}, err
	}
	record, err := s.stageBlob(ctx, encoded)
	if err != nil {
		return runtime.FleetHead{}, err
	}
	selected, _ := json.Marshal(head)
	var previous []byte
	if publication.Expected != (runtime.FleetHead{}) {
		previous, _ = json.Marshal(publication.Expected)
	}
	grant, err := encodeFleetGrant(publication.Grant)
	if err != nil {
		return runtime.FleetHead{}, err
	}
	generationKey := s.generationKey(head.GenerationID)
	indexed, _, err := s.store.ReadWithLifetime(ctx, generationKey, 4096)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return runtime.FleetHead{}, err
	}
	nextIndex := selected
	if indexed != nil {
		if err := s.checkGenerationContent(ctx, publication.Generation); err != nil {
			return runtime.FleetHead{}, err
		}
		nextIndex = indexed
	}
	err = s.store.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{
		{Key: s.prefix + "lease", ExpectedValue: grant, NewValue: grant},
		{Key: s.prefix + "head", ExpectedValue: previous, NewValue: selected},
		{Key: s.publicationKey(head), NewValue: record},
		{Key: generationKey, ExpectedValue: indexed, NewValue: nextIndex},
	}, s.prefix+"lease")
	if err == nil {
		return head, nil
	}
	if !errors.Is(err, storage.ErrConflict) {
		return runtime.FleetHead{}, err
	}
	// Only the atomic commit creates this immutable receipt. Staged chunks cannot prove success.
	committed, _, readErr := s.store.ReadWithLifetime(ctx, s.publicationKey(head), fleetDescriptorMaxBytes)
	if readErr == nil && bytes.Equal(committed, record) {
		return head, nil
	}
	if readErr != nil && !errors.Is(readErr, storage.ErrNotFound) {
		return runtime.FleetHead{}, readErr
	}
	return runtime.FleetHead{}, fleetStoreConflict("the original live grant or exact catalog predecessor changed")
}

func (s *FleetStore) checkGenerationContent(ctx context.Context, generation catalogs.Generation) error {
	current, err := s.Get(ctx, generation.Manifest.GenerationID)
	if err != nil {
		return err
	}
	existing, err := json.Marshal(current)
	if err != nil {
		return err
	}
	proposed, err := json.Marshal(generation)
	if err != nil {
		return err
	}
	if !bytes.Equal(existing, proposed) {
		return fleetStoreConflict("the generation ID already identifies different content")
	}
	return nil
}

func (s *FleetStore) publicationKey(head runtime.FleetHead) string {
	encoded, _ := json.Marshal(head)
	return s.prefix + "publication:" + strconv.FormatUint(head.Revision, 10) + ":" + payloadDigest(encoded)
}

func (s *FleetStore) generationKey(id string) string {
	return s.prefix + "generation:" + payloadDigest([]byte(id))
}

func fleetReadError(err error, id string) error {
	if errors.Is(err, storage.ErrNotFound) {
		return &starmaperrors.NotFoundError{Resource: "fleet publication", ID: id}
	}
	return err
}

func fleetStoreConflict(message string) error {
	return &starmaperrors.ConflictError{Resource: "catalog fleet", Message: message}
}

var _ runtime.FleetStore = (*FleetStore)(nil)
