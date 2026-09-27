package catalog

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/agentstation/starmap/pkg/catalogs"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/runtime"

	"github.com/agentstation/starport/internal/catalog/recovery"
	"github.com/agentstation/starport/internal/storage"
)

const fleetPublicationResource = "fleet publication"

type fleetRecoveryWitness interface {
	Approved(context.Context, string) (recovery.Record, error)
	CheckBootstrap(context.Context, recovery.Record) error
	ConsumeBootstrap(context.Context, recovery.Record) error
}

// FleetStore binds shared catalog publication to an independent recovery approval.
// It uses native backend expiry and retains complete acquisition inputs outside local directories.
type FleetStore struct {
	store    storage.IncarnationStore
	witness  fleetRecoveryWitness
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
	if errors.Is(err, storage.ErrNotFound) {
		if err := s.checkEmptyHead(ctx); err != nil {
			return runtime.FleetHead{}, err
		}
	}
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
	for range fleetRetentionAttempts {
		head, err := s.CurrentHead(ctx)
		if err != nil {
			return runtime.FleetSnapshot{}, err
		}
		snapshot, err := s.Publication(ctx, head)
		if errors.Is(err, starmaperrors.ErrNotFound) {
			current, readErr := s.CurrentHead(ctx)
			if readErr != nil {
				return runtime.FleetSnapshot{}, readErr
			}
			if current != head {
				continue
			}
			return runtime.FleetSnapshot{}, errors.New("selected fleet publication is unavailable")
		}
		return snapshot, err
	}
	return runtime.FleetSnapshot{}, fleetStoreConflict("catalog selection changed during the read")
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
	m := &fleetMaintenance{owner: s}
	if err := m.load(ctx); err != nil {
		return runtime.FleetSnapshot{}, err
	}
	blob, ok := m.publication(head)
	if !ok {
		return runtime.FleetSnapshot{}, &starmaperrors.NotFoundError{Resource: fleetPublicationResource, ID: head.GenerationID}
	}
	snapshot, err := m.read(ctx, blob)
	if errors.Is(err, storage.ErrNotFound) {
		if loadErr := m.load(ctx); loadErr != nil {
			return snapshot, loadErr
		}
		if _, retained := m.publication(head); !retained {
			return snapshot, &starmaperrors.NotFoundError{Resource: fleetPublicationResource, ID: head.GenerationID}
		}
	}
	return snapshot, err
}

// Get reads an immutable catalog from a retained publication.
func (s *FleetStore) Get(ctx context.Context, id string) (catalogs.Generation, error) {
	if err := s.checkApproval(ctx); err != nil {
		return catalogs.Generation{}, err
	}
	m := &fleetMaintenance{owner: s}
	if err := m.load(ctx); err != nil {
		return catalogs.Generation{}, err
	}
	blob, ok := m.generation(id)
	if !ok {
		return catalogs.Generation{}, &starmaperrors.NotFoundError{Resource: "fleet generation", ID: id}
	}
	snapshot, err := s.Publication(ctx, blob.Head)
	return snapshot.Publication.Generation, err
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
	m, err := s.maintain(ctx)
	if err != nil {
		return runtime.FleetHead{}, err
	}
	defer m.finish()
	if committed, ok := m.publication(head); ok {
		prior, readErr := m.read(ctx, committed)
		if readErr != nil {
			return runtime.FleetHead{}, readErr
		}
		original, marshalErr := json.Marshal(prior)
		if marshalErr != nil {
			return runtime.FleetHead{}, marshalErr
		}
		if bytes.Equal(original, encoded) {
			return head, nil
		}
		return runtime.FleetHead{}, fleetStoreConflict("the retained receipt identifies a different request")
	}
	current, previous, err := m.readHead(ctx)
	if err != nil {
		return runtime.FleetHead{}, err
	}
	if current != publication.Expected {
		return runtime.FleetHead{}, fleetStoreConflict("the exact catalog predecessor changed")
	}
	grant, err := encodeFleetGrant(publication.Grant)
	if err != nil {
		return runtime.FleetHead{}, err
	}
	if err = m.mutate(ctx, []storage.CompareAndSwapMutation{{Key: s.prefix + "lease", ExpectedValue: grant, NewValue: grant}}, s.prefix+"lease"); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return runtime.FleetHead{}, fleetStoreConflict("the original refresh grant expired or changed")
		}
		return runtime.FleetHead{}, err
	}
	if existing, ok := m.generation(head.GenerationID); ok {
		prior, err := m.read(ctx, existing)
		if err != nil {
			return runtime.FleetHead{}, err
		}
		before, _ := json.Marshal(prior.Publication.Generation)
		after, _ := json.Marshal(publication.Generation)
		if !bytes.Equal(before, after) {
			return runtime.FleetHead{}, fleetStoreConflict("the generation ID already identifies different content")
		}
	}
	if err = m.capacity(len(encoded)); err != nil {
		return runtime.FleetHead{}, err
	}
	blob, err := m.stageBlob(ctx, snapshot, encoded, grant)
	if err != nil {
		return runtime.FleetHead{}, err
	}
	selected, _ := json.Marshal(head)
	record, _ := json.Marshal(blob)
	m.inventory.Pending = nil
	m.inventory.Entries = append(m.inventory.Entries, blob)
	inventory, err := json.Marshal(m.inventory)
	if err != nil {
		return runtime.FleetHead{}, err
	}
	var initialized []byte
	if publication.Expected != (runtime.FleetHead{}) {
		initialized = []byte("fleet-head/1")
	} else if err := s.witness.ConsumeBootstrap(ctx, s.approval); err != nil {
		return runtime.FleetHead{}, err
	}
	err = m.mutate(ctx, []storage.CompareAndSwapMutation{
		{Key: s.prefix + "lease", ExpectedValue: grant, NewValue: grant},
		{Key: s.prefix + "head-initialized", ExpectedValue: initialized, NewValue: []byte("fleet-head/1")},
		{Key: s.prefix + "head", ExpectedValue: previous, NewValue: selected},
		{Key: s.publicationKey(head), NewValue: record},
		{Key: s.prefix + "inventory", ExpectedValue: m.encoded, NewValue: inventory},
	}, s.prefix+"lease")
	if err == nil {
		return head, nil
	}
	if errors.Is(err, storage.ErrConflict) {
		return runtime.FleetHead{}, fleetStoreConflict("the original live grant, maintenance lease, or exact predecessor changed")
	}
	return runtime.FleetHead{}, err
}

func (s *FleetStore) publicationKey(head runtime.FleetHead) string {
	encoded, _ := json.Marshal(head)
	return s.prefix + "publication:" + strconv.FormatUint(head.Revision, 10) + ":" + payloadDigest(encoded)
}

func fleetReadError(err error, id string) error {
	if errors.Is(err, storage.ErrNotFound) {
		return &starmaperrors.NotFoundError{Resource: fleetPublicationResource, ID: id}
	}
	return err
}

func fleetStoreConflict(message string) error {
	return &starmaperrors.ConflictError{Resource: "catalog fleet", Message: message}
}

var _ runtime.FleetStore = (*FleetStore)(nil)

// checkEmptyHead distinguishes an unused publication store from lost or uncertain state.
func (s *FleetStore) checkEmptyHead(ctx context.Context) error {
	if err := s.witness.CheckBootstrap(ctx, s.approval); err != nil {
		return err
	}
	_, _, err := s.store.ReadWithLifetime(ctx, s.prefix+"head-initialized", 64)
	if !errors.Is(err, storage.ErrNotFound) {
		return fleetStoreConflict("initialized catalog head is missing or uncertain; recovery is required")
	}
	m := &fleetMaintenance{owner: s}
	if err := m.load(ctx); err != nil {
		return err
	}
	if len(m.inventory.Entries) != 0 {
		return fleetStoreConflict("retained catalog publications have no selected head; recovery is required")
	}
	return nil
}
