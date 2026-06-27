//go:build !unix

package core

// flockFile is a no-op on non-Unix platforms (e.g., Windows).
// Cross-process concurrent write protection becomes best-effort (relying on rename atomicity, but concurrent overwrites may still lose data).
// Windows is rare for production coding agents; implement LockFileEx if needed later.
func flockFile(_ string) (func(), error) {
	return func() {}, nil
}
