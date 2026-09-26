package catalog

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/runtime"

	"github.com/agentstation/starport/internal/storage"
)

type fleetAcceptance struct {
	Head    runtime.FleetHead      `json:"head"`
	History []GenerationIndexEntry `json:"history"`
}

// AcceptedPublication reads the publication that passed Starport route validation.
func (s *FleetStore) AcceptedPublication(ctx context.Context) (runtime.FleetSnapshot, error) {
	if err := s.checkApproval(ctx); err != nil {
		return runtime.FleetSnapshot{}, err
	}
	accepted, _, err := s.readAcceptance(ctx)
	if err != nil {
		return runtime.FleetSnapshot{}, fleetReadError(err, "accepted")
	}
	return s.selectedPublication(ctx, accepted.Head)
}

// AcceptPublication selects a validated candidate under its original live acquisition grant.
// It compares the complete candidate and accepted predecessors in the same native transaction.
func (s *FleetStore) AcceptPublication(ctx context.Context, selected, expected runtime.FleetHead) error {
	if err := s.checkApproval(ctx); err != nil {
		return err
	}
	if err := selected.Validate(); err != nil {
		return err
	}
	if err := expected.Validate(); err != nil {
		return err
	}
	if selected.Identity != s.identity || (expected != (runtime.FleetHead{}) && expected.Identity != s.identity) {
		return fleetStoreConflict("catalog acceptance belongs to another recovery identity")
	}
	current, encoded, err := s.readAcceptance(ctx)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if current.Head == selected {
		return nil
	}
	if current.Head != expected || selected.Revision <= expected.Revision {
		return fleetStoreConflict("the accepted catalog predecessor changed or the candidate regressed")
	}
	snapshot, err := s.Publication(ctx, selected)
	if err != nil {
		return err
	}
	entry, err := fleetIndexEntry(snapshot.Publication.Generation)
	if err != nil {
		return err
	}
	history := append(current.History, entry)
	if len(history) > catalogGenerationIndexCap {
		history = history[len(history)-catalogGenerationIndexCap:]
	}
	next, err := json.Marshal(fleetAcceptance{Head: selected, History: history})
	if err != nil {
		return err
	}
	grant, err := encodeFleetGrant(snapshot.Publication.Grant)
	if err != nil {
		return err
	}
	head, _ := json.Marshal(selected)
	err = s.store.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{
		{Key: s.prefix + "lease", ExpectedValue: grant, NewValue: grant},
		{Key: s.prefix + "head", ExpectedValue: head, NewValue: head},
		{Key: s.prefix + "accepted", ExpectedValue: encoded, NewValue: next},
	}, s.prefix+"lease")
	if errors.Is(err, storage.ErrConflict) {
		return fleetStoreConflict("the original grant, candidate head, or accepted head changed during route validation")
	}
	return err
}

// AcceptedHistory returns a bounded index from the same record as the accepted head.
func (s *FleetStore) AcceptedHistory(ctx context.Context) ([]GenerationIndexEntry, error) {
	if err := s.checkApproval(ctx); err != nil {
		return nil, err
	}
	accepted, _, err := s.readAcceptance(ctx)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	return accepted.History, err
}

func (s *FleetStore) readAcceptance(ctx context.Context) (fleetAcceptance, []byte, error) {
	data, _, err := s.store.ReadWithLifetime(ctx, s.prefix+"accepted", fleetDescriptorMaxBytes)
	if err != nil {
		return fleetAcceptance{}, nil, err
	}
	var accepted fleetAcceptance
	if err := json.Unmarshal(data, &accepted); err != nil {
		return accepted, nil, err
	}
	if err := accepted.Head.Validate(); err != nil {
		return accepted, nil, err
	}
	if accepted.Head.Identity != s.identity || len(accepted.History) == 0 || len(accepted.History) > catalogGenerationIndexCap ||
		accepted.History[len(accepted.History)-1].GenerationID != accepted.Head.GenerationID {
		return accepted, nil, errors.New("invalid fleet acceptance record")
	}
	return accepted, data, nil
}

func fleetIndexEntry(generation catalogs.Generation) (GenerationIndexEntry, error) {
	decoded, err := catalogs.DecodeCatalogPayload(generation.Payload)
	if err != nil {
		return GenerationIndexEntry{}, err
	}
	semantic, err := catalogs.CatalogSemanticChecksum(decoded)
	if err != nil {
		return GenerationIndexEntry{}, err
	}
	return GenerationIndexEntry{GenerationID: generation.Manifest.GenerationID, GeneratedAt: generation.Manifest.GeneratedAt,
		PayloadChecksum: generation.Manifest.Payload.Checksum, SemanticChecksum: semantic}, nil
}
