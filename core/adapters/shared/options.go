package shared

import "sort"

// SortedStringKeys returns the keys of m in ascending order. Adapters use it to emit variant HTTP
// header overrides in a deterministic order (stable request shape, stable debug logs).
func SortedStringKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SortedAnyKeys returns the keys of m in ascending order. Adapters use it to emit variant JSON body
// overrides in a deterministic order.
func SortedAnyKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
