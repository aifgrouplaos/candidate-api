// Package utils provides domain-independent helpers. Business rules belong in internal modules.
package utils

// Coalesce returns the first non-zero value, or the zero value if none is provided.
func Coalesce[T comparable](values ...T) T {
	var zero T
	for _, value := range values {
		if value != zero {
			return value
		}
	}
	return zero
}

// Unique returns a new slice with duplicates removed, preserving first-seen order.
// A nil input returns nil. The input is never modified.
func Unique[T comparable](values []T) []T {
	if values == nil {
		return nil
	}
	result := make([]T, 0, len(values))
	seen := make(map[T]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}
