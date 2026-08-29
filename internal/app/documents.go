package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/proxy"
)

// storedDocuments adapts the file service to the document port the proxy
// declares. It is the only place the two vocabularies meet: the proxy asks for
// bytes by account and identifier, and the file service answers with the
// record it agreed to hand out.
type storedDocuments struct {
	service *files.Service
}

// ResolveDocument implements proxy.FileResolver.
//
// A file this account does not hold reports absent rather than an error, and a
// file another account holds reaches the same answer through the same path:
// Open takes the account, so a foreign identifier is a miss inside the service
// rather than a check this adapter could forget to make.
func (d storedDocuments) ResolveDocument(
	ctx context.Context,
	account, id string,
) (proxy.StoredDocument, bool, error) {
	record, reader, err := d.service.Open(ctx, account, id)
	if errors.Is(err, files.ErrFileNotFound) {
		return proxy.StoredDocument{}, false, nil
	}
	if err != nil {
		return proxy.StoredDocument{}, false, err
	}
	defer func() { _ = reader.Close() }()

	// The record states the size the write landed, so the read is bounded by
	// what the account already paid storage for rather than by whatever the
	// byte store hands back.
	data, err := io.ReadAll(io.LimitReader(reader, record.Bytes))
	if err != nil {
		return proxy.StoredDocument{}, false, fmt.Errorf("read stored file %q: %w", id, err)
	}
	return proxy.StoredDocument{Filename: record.Filename, Data: data}, true, nil
}
