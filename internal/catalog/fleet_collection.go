package catalog

import (
	"context"
	"errors"
	"slices"

	catalogstorage "github.com/agentstation/starmap/pkg/catalogs/storage"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/storage"
)

// Collect applies the caller's retention policy under the native maintenance lease.
// Publication does not collect committed data. Dry runs do not recover pending deletions.
func (s *FleetStore) Collect(ctx context.Context, request catalogstorage.RetentionRequest) (catalogstorage.RetentionReport, error) {
	var report catalogstorage.RetentionReport
	if err := request.Validate(); err != nil {
		return report, err
	}
	m, err := s.beginMaintenance(ctx, false)
	if err != nil {
		return report, err
	}
	defer m.finish()
	limit := request.ScanEntries
	if limit == 0 {
		limit = catalogstorage.DefaultRetentionScanEntries
	}
	count := len(m.inventory.Entries)
	if m.inventory.Pending != nil {
		count++
	}
	if count > limit {
		return report, errors.New("fleet inventory exceeds the collection scan limit")
	}
	keep, guards, err := m.collectionProtection(ctx, request)
	if err != nil {
		return report, err
	}
	report.Before = fleetGenerationUsage(m.inventory.Entries)
	report.After = report.Before
	protected := slices.DeleteFunc(slices.Clone(m.inventory.Entries), func(b fleetBlob) bool { return !keep[b.ID] })
	report.Protected = fleetGenerationUsage(protected)
	report.OverLimit = report.Protected.Generations > request.MaxGenerations || report.Protected.Bytes > request.MaxBytes
	retained, removed := fleetCollectionSelection(m.inventory.Entries, keep, request)
	report.Projected = fleetGenerationUsage(retained)
	remaining := map[string]bool{}
	for _, b := range retained {
		remaining[b.Head.GenerationID] = true
	}
	for _, b := range removed {
		id := b.Head.GenerationID
		if !remaining[id] && !slices.Contains(report.Candidates, id) {
			report.Candidates = append(report.Candidates, id)
		}
	}
	publications := &catalogstorage.PublicationRetentionReport{
		Before:      fleetPublicationUsage(m.inventory.Entries, m.inventory.Pending, len(m.inventory.Readers)),
		Projected:   fleetPublicationUsage(retained, nil, len(m.inventory.Readers)),
		Protected:   fleetPublicationUsage(protected, nil, len(m.inventory.Readers)),
		MaxReceipts: fleetRetentionMaxEntries, MaxEncodedBytes: fleetRetentionMaxBytes,
	}
	publications.After = publications.Before
	report.Publications = publications
	budget := request.InputMaxBytes
	if budget == 0 {
		budget = catalogstorage.DefaultRetentionInputMaxBytes
	}
	// Reserve the maximum receipt read and all chunk bytes before deleting anything.
	var recoveryBytes int64
	for _, b := range removed {
		recoveryBytes += int64(b.Record.Size) + fleetDescriptorMaxBytes
	}
	if m.inventory.Pending != nil {
		recoveryBytes += int64(m.inventory.Pending.Record.Size) + fleetDescriptorMaxBytes
	}
	if recoveryBytes > budget {
		return report, errors.New("fleet collection exceeds the recovery input byte limit; increase catalog_retention.input_max_bytes")
	}
	if request.DryRun {
		return report, nil
	}
	// Keep partial results accurate if a native operation fails after a durable change.
	update := func() {
		report.After = fleetGenerationUsage(m.inventory.Entries)
		publications.After = fleetPublicationUsage(m.inventory.Entries, m.inventory.Pending, len(m.inventory.Readers))
		for _, id := range report.Candidates {
			if _, exists := m.generation(id); !exists {
				report.Removed = append(report.Removed, id)
			}
		}
	}
	if err = m.recover(ctx); err != nil {
		return report, err
	}
	for _, blob := range removed {
		i := slices.IndexFunc(m.inventory.Entries, func(b fleetBlob) bool { return b.ID == blob.ID })
		previous := slices.Clone(m.inventory.Entries)
		m.inventory.Entries = slices.Delete(m.inventory.Entries, i, i+1)
		m.inventory.Pending = &blob
		if err = m.save(ctx, guards...); err != nil {
			// A lost response is ambiguous. Reload before reporting the removed entries.
			m.inventory.Entries, m.inventory.Pending = previous, nil
			loadErr := m.load(ctx)
			update()
			return report, errors.Join(err, loadErr)
		}
		if err = m.recover(ctx); err != nil {
			update()
			return report, err
		}
	}
	update()
	return report, nil
}

func (m *fleetMaintenance) collectionProtection(ctx context.Context, request catalogstorage.RetentionRequest) (map[string]bool, []storage.CompareAndSwapMutation, error) {
	head, headBytes, err := m.readHead(ctx)
	if err != nil {
		return nil, nil, err
	}
	if head.GenerationID != request.ExpectedGenerationID {
		return nil, nil, fleetStoreConflict("the collection predecessor changed")
	}
	accepted, acceptedBytes, err := m.owner.readAcceptance(ctx)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return nil, nil, err
	}
	keep := map[string]bool{}
	for _, head := range []runtime.FleetHead{head, accepted.Head} {
		if head == (runtime.FleetHead{}) {
			continue
		}
		b, ok := m.publication(head)
		if !ok {
			return nil, nil, errors.New("a protected fleet publication is missing")
		}
		keep[b.ID] = true
	}
	required := slices.Clone(request.RequiredGenerationIDs)
	for _, entry := range accepted.History {
		required = append(required, entry.GenerationID)
	}
	for _, id := range required {
		b, ok := m.generation(id)
		if !ok {
			return nil, nil, &starmaperrors.NotFoundError{Resource: "required fleet generation", ID: id}
		}
		keep[b.ID] = true
	}
	for _, id := range m.inventory.Readers {
		keep[id] = true
	}
	for _, b := range m.inventory.Entries[max(0, len(m.inventory.Entries)-catalogGenerationIndexCap):] {
		keep[b.ID] = true
	}
	guards := []storage.CompareAndSwapMutation{
		{Key: m.owner.prefix + "head", ExpectedValue: headBytes, NewValue: headBytes},
		{Key: m.owner.prefix + "accepted", ExpectedValue: acceptedBytes, NewValue: acceptedBytes},
	}
	return keep, guards, nil
}

// fleetCollectionSelection keeps one newest receipt per retained generation.
func fleetCollectionSelection(entries []fleetBlob, protected map[string]bool, request catalogstorage.RetentionRequest) ([]fleetBlob, []fleetBlob) {
	keep := map[string]bool{}
	generations := map[string]bool{}
	for _, b := range entries {
		if protected[b.ID] {
			keep[b.ID], generations[b.Head.GenerationID] = true, true
		}
	}
	for _, b := range slices.Backward(entries) {
		if !generations[b.Head.GenerationID] {
			keep[b.ID], generations[b.Head.GenerationID] = true, true
		}
	}
	selected := func() []fleetBlob {
		return slices.DeleteFunc(slices.Clone(entries), func(b fleetBlob) bool { return !keep[b.ID] })
	}
	usage := fleetGenerationUsage(selected())
	for _, b := range entries {
		if usage.Generations <= request.MaxGenerations && usage.Bytes <= request.MaxBytes {
			break
		}
		if !keep[b.ID] || protected[b.ID] {
			continue
		}
		delete(keep, b.ID)
		usage = fleetGenerationUsage(selected())
	}
	return selected(), slices.DeleteFunc(slices.Clone(entries), func(b fleetBlob) bool { return keep[b.ID] })
}

func fleetGenerationUsage(entries []fleetBlob) catalogstorage.RetentionUsage {
	var usage catalogstorage.RetentionUsage
	seen := map[string]bool{}
	for _, b := range entries {
		if !seen[b.Head.GenerationID] {
			usage.Generations++
			usage.Bytes += b.GenerationBytes
			seen[b.Head.GenerationID] = true
		}
	}
	return usage
}

func fleetPublicationUsage(entries []fleetBlob, pending *fleetBlob, readers int) catalogstorage.PublicationRetentionUsage {
	usage := catalogstorage.PublicationRetentionUsage{Receipts: len(entries), ReaderClaims: readers}
	for _, b := range entries {
		usage.EncodedBytes += int64(b.Record.Size)
		usage.RecoveryBytes += b.RecoveryBytes
	}
	if pending != nil {
		usage.EncodedBytes += int64(pending.Record.Size)
		usage.RecoveryBytes += pending.RecoveryBytes
	}
	return usage
}

var _ catalogstorage.GenerationCollector = (*FleetStore)(nil)
