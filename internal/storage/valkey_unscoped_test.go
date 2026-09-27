package storage

// openUnscopedValkeyForTest preserves raw adapter and wire-contract fixtures.
func openUnscopedValkeyForTest(config ValkeyConfig) (KVStore, error) {
	return openValkey(config, "")
}
