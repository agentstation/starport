package blob

import (
	"fmt"
	"strings"
)

// MaxKeyLength bounds a key. The bound keeps a key inside the length every
// backing medium accepts, including a filesystem path component and an object
// store key.
const MaxKeyLength = 256

// ValidateKey reports whether the key is one this package stores under.
//
// A key is opaque: it carries no structure a store reads. The rules exist so
// that one key means the same object on every backend. A path separator is the
// case that matters most, because a filesystem would read it as a directory
// step and an object store would read it as a prefix, and the same key would
// then name two different places.
func ValidateKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: the key is empty", ErrInvalidKey)
	}
	if len(key) > MaxKeyLength {
		return fmt.Errorf("%w: the key is %d bytes, and the bound is %d", ErrInvalidKey, len(key), MaxKeyLength)
	}
	if strings.ContainsAny(key, `/\`) {
		return fmt.Errorf("%w: the key holds a path separator", ErrInvalidKey)
	}
	if key == "." || key == ".." {
		return fmt.Errorf("%w: the key names a directory step", ErrInvalidKey)
	}
	for i := 0; i < len(key); i++ {
		if !keyByteAllowed(key[i]) {
			return fmt.Errorf("%w: the key holds the byte %q at offset %d", ErrInvalidKey, key[i], i)
		}
	}
	return nil
}

// keyByteAllowed holds the allowed set. It is a list rather than a set of
// refusals, because a new hazard on a new medium must fail closed.
func keyByteAllowed(b byte) bool {
	switch {
	case b >= '0' && b <= '9':
		return true
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b == '-' || b == '_' || b == '.':
		return true
	default:
		return false
	}
}
