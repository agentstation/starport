package storage

import "context"

// Populated adoption supports only Valkey fleet storage. Badger refuses the capability.
func (*badgerTransfer) ObserveImportControls(context.Context) (ImportControls, error) {
	return ImportControls{}, ErrPopulatedImportUnsupported
}

func (*badgerTransfer) ClaimPopulated(context.Context, []byte, ImportControls, CapturedRecordSource) error {
	return ErrPopulatedImportUnsupported
}

var _ PopulatedImportClaimer = (*badgerTransfer)(nil)
