package storage

// KeyPrefixResponse is the prefix for cached responses. The response cache
// manager composes record keys from it; Starmap owns model and provider
// facts, so no model metadata is keyed here.
const KeyPrefixResponse = "response:"

// KeyPrefixExtraction is the prefix for cached document text. A parser engine
// writes here and the response cache writes above, and the two never share a
// key: one holds what a model answered, and this one holds what a document
// says.
const KeyPrefixExtraction = "extraction:"
