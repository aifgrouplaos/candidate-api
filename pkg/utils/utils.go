// Package utils provides domain-independent helpers. Business rules belong in internal modules.
package utils

import "strings"

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

// ContainsPattern returns a LIKE pattern matching values that contain search literally.
func ContainsPattern(search string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(search) + "%"
}

// Optional trims a nullable string and treats blank as null.
func Optional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
