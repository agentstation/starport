package storage

// KeyPrefixResponse is the prefix for cached responses. The response cache
// manager composes record keys from it; Starmap owns model and provider
// facts, so no model metadata is keyed here.
const KeyPrefixResponse = "response:"
