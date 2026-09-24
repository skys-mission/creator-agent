// Package auth issues and verifies the bearer token guarding the gRPC network
// face. This is gate 2 of the three-gate hardening in docs/architecture.md §8:
// the token proves identity on the wire when TLS is not in play (h2c).
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tokenBytes is the entropy of a freshly issued token (256 bits).
const tokenBytes = 32

// Store holds the bearer token and its on-disk location. A Store is safe for
// concurrent use once Ensure has returned.
type Store struct {
	path  string
	value string
}

// New returns a Store bound to path. The token is not read until Ensure.
func New(path string) *Store {
	return &Store{path: path}
}

// Path returns the token file location.
func (s *Store) Path() string { return s.path }

// Ensure loads the token from disk, creating a fresh 0600 token file when it
// does not exist. Refuses to run with an empty token file (fail-closed).
func (s *Store) Ensure() error {
	b, err := os.ReadFile(s.path)
	if err == nil {
		v := strings.TrimSpace(string(b))
		if v == "" {
			return fmt.Errorf("auth: token file %s is empty (delete it to reissue)", s.path)
		}
		s.value = v
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("auth: read token file: %w", err)
	}

	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("auth: generate token: %w", err)
	}
	v := hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("auth: create token dir: %w", err)
	}
	if err := os.WriteFile(s.path, []byte(v+"\n"), 0o600); err != nil {
		return fmt.Errorf("auth: write token file: %w", err)
	}
	s.value = v
	return nil
}

// Value returns the token (empty before Ensure).
func (s *Store) Value() string { return s.value }

// Check reports whether got equals the stored token, in constant time. An
// unensured store accepts nothing.
func (s *Store) Check(got string) bool {
	if s.value == "" || got == "" {
		return false
	}
	// Hash both sides so the comparison runs in constant time regardless of
	// the presented token's length.
	a := sha256.Sum256([]byte(s.value))
	b := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
