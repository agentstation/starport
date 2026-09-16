package app

import "context"

func (b *runtimeBuilder) validateCatalogStorage() error {
	return catalogSettings(b.config).ValidateStorageSelection(context.Background())
}
