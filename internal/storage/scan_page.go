package storage

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/dgraph-io/badger/v4"
)

// ErrInvalidScan reports an invalid scan cursor or work hint.
var ErrInvalidScan = errors.New("invalid storage scan")

// KeyPage carries one native scan response. An empty Next ends the iteration.
// Keys can be empty before completion or repeat across pages.
// The count argument controls work, not the native response size.
//
// Cursors belong to one prefix, store, and process lifetime. Restart from empty
// after reconnecting to a replacement backend. Consumers must tolerate replay.
type KeyPage struct {
	Keys []string
	Next string
}

func scanStart(prefix, cursor string, count int) (string, error) {
	if count <= 0 || count > 1000 {
		return "", ErrInvalidScan
	}
	if cursor == "" {
		return prefix, nil
	}
	value, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !strings.HasPrefix(string(value), prefix) {
		return "", ErrInvalidScan
	}
	return string(value), nil
}

// ScanPage reads one page after the prior key in a Badger snapshot.
func (s *BadgerStore) ScanPage(ctx context.Context, prefix, cursor string, count int) (KeyPage, error) {
	start, err := scanStart(prefix, cursor, count)
	if err != nil {
		return KeyPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return KeyPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return KeyPage{}, ErrStorageClosed
	}
	var page KeyPage
	err = s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = []byte(prefix)
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek([]byte(start)); it.ValidForPrefix([]byte(prefix)); it.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			item := it.Item()
			key := string(item.Key())
			if item.IsDeletedOrExpired() || cursor != "" && key == start {
				continue
			}
			if len(page.Keys) == count {
				page.Next = base64.RawURLEncoding.EncodeToString([]byte(page.Keys[len(page.Keys)-1]))
				break
			}
			page.Keys = append(page.Keys, key)
		}
		return nil
	})
	return page, err
}

// ScanPage reads one native Valkey cursor step without truncating its response.
func (v *ValkeyStore) ScanPage(ctx context.Context, prefix, cursor string, count int) (KeyPage, error) {
	if count <= 0 || count > 1000 {
		return KeyPage{}, ErrInvalidScan
	}
	var position uint64
	if cursor != "" {
		var err error
		position, err = strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			return KeyPage{}, ErrInvalidScan
		}
	}
	// MATCH uses glob syntax. The contract takes a literal prefix.
	pattern := strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?", "[", "\\[", "]", "\\]").Replace(v.prefix+prefix) + "*"
	response, err := v.do(ctx, v.client.B().Scan().Cursor(position).Match(pattern).Count(int64(count)).Build()).AsScanEntry()
	if err != nil {
		return KeyPage{}, err
	}
	var page KeyPage
	if response.Cursor != 0 {
		page.Next = strconv.FormatUint(response.Cursor, 10)
	}
	for _, key := range response.Elements {
		if logical, ok := strings.CutPrefix(key, v.prefix); ok && strings.HasPrefix(logical, prefix) {
			page.Keys = append(page.Keys, logical)
		}
	}
	return page, nil
}

// ScanPage gives the in-memory test store the ordered cursor contract.
func (m *MockStore) ScanPage(ctx context.Context, prefix, cursor string, count int) (KeyPage, error) {
	start, err := scanStart(prefix, cursor, count)
	if err != nil {
		return KeyPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return KeyPage{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return KeyPage{}, ErrStorageClosed
	}
	var keys []string
	for key := range m.data {
		if strings.HasPrefix(key, prefix) && (cursor == "" || key > start) && m.checkExpired(key) == nil {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	page := KeyPage{Keys: keys}
	if len(keys) > count {
		page.Keys = keys[:count]
		page.Next = base64.RawURLEncoding.EncodeToString([]byte(keys[count-1]))
	}
	return page, nil
}
