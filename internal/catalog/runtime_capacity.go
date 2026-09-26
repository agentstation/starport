package catalog

import "errors"

// ErrRuntimeGenerationCapacity defers activation until retained requests finish.
var ErrRuntimeGenerationCapacity = errors.New("runtime generation capacity is exhausted")
