package storage

import "fmt"

func validateCompareAndSwapMutations(mutations []CompareAndSwapMutation) error {
	seen := make(map[string]struct{}, len(mutations))
	for index, mutation := range mutations {
		if mutation.Key == "" {
			return fmt.Errorf("%w: mutation %d has an empty key", ErrInvalidMutation, index)
		}
		if mutation.TTL < 0 {
			return fmt.Errorf("%w: mutation %d has a negative TTL", ErrInvalidMutation, index)
		}
		if _, exists := seen[mutation.Key]; exists {
			return fmt.Errorf("%w: key %q is duplicated", ErrInvalidMutation, mutation.Key)
		}
		seen[mutation.Key] = struct{}{}
	}
	return nil
}
