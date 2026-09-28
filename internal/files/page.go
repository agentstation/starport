package files

import (
	"context"
	"errors"
	"github.com/agentstation/starport/internal/storage"
)

// RecordPage carries one native scan step. An empty continuation ends the pass.
// Scanned distinguishes a failed scan from a valid page with no readable records.
type RecordPage struct {
	Records []File
	Next    string
	Scanned bool
}

func (r *repository) Page(ctx context.Context, account, cursor string) (RecordPage, error) {
	prefix := StoragePrefix
	if account != "" {
		prefix = accountPrefix(account)
	}
	keys, err := r.store.ScanPage(ctx, prefix, cursor, 256)
	if err != nil {
		return RecordPage{}, err
	}
	page := RecordPage{Next: keys.Next, Scanned: true}
	var failure error
	for _, key := range keys.Keys {
		data, err := r.store.GetBounded(ctx, key, 16384)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			if failure == nil {
				failure = err
			}
			continue
		}
		file, err := decodeFile(data)
		if err != nil {
			if failure == nil {
				failure = err
			}
			continue
		}
		if storageKey(file.Account, file.ID) != key {
			if failure == nil {
				failure = ErrCorruptRecord
			}
			continue
		}
		page.Records = append(page.Records, file)
	}
	return page, failure
}
