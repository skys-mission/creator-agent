package core

import (
	"crypto/rand"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// SessionStore is the persistence abstraction for conversation history.
type SessionStore interface {
	Load(id string) ([]Message, error)
	Save(id string, msgs []Message) error
	SaveWithMeta(id, title string, msgs []Message) error
	Clear(id string) error
	List() ([]SessionInfo, error)
}

// SessionMetaMutator is an optional capability of a SessionStore.
type SessionMetaMutator interface {
	SetPinned(id string, pinned bool) error
	Rename(id, title string) error
}

type memEntry struct {
	title     string
	updatedAt time.Time
	msgs      []Message
	pinned    bool
}

// MemoryStore is the in-memory implementation (default, backward-compatible with MVP behavior).
type MemoryStore struct {
	mu  sync.Mutex
	mem map[string]memEntry
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{mem: make(map[string]memEntry)} }

func (m *MemoryStore) Load(id string) ([]Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.mem[id]; ok {
		return e.msgs, nil
	}
	return nil, nil
}

func (m *MemoryStore) Save(id string, msgs []Message) error {
	return m.SaveWithMeta(id, "", msgs)
}

func (m *MemoryStore) SaveWithMeta(id, title string, msgs []Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	prev, hadPrev := m.mem[id]
	t := title
	if t == "" {
		if hadPrev {
			t = prev.title
		} else {
			t = DeriveTitle(msgs, now)
		}
	}
	pinned := false
	if hadPrev {
		pinned = prev.pinned
	}
	m.mem[id] = memEntry{title: t, updatedAt: now, msgs: msgs, pinned: pinned}
	return nil
}

func (m *MemoryStore) SetPinned(id string, pinned bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.mem[id]
	if !ok {
		return nil
	}
	e.pinned = pinned
	m.mem[id] = e
	return nil
}

func (m *MemoryStore) Rename(id, title string) error {
	if title == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.mem[id]
	if !ok {
		return nil
	}
	e.title = title
	m.mem[id] = e
	return nil
}

func (m *MemoryStore) LoadMeta(id string) (title string, updatedAt time.Time, pinned bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.mem[id]
	if !ok {
		return "", time.Time{}, false, nil
	}
	return e.title, e.updatedAt, e.pinned, nil
}

func (m *MemoryStore) Clear(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.mem, id)
	return nil
}

func (m *MemoryStore) List() ([]SessionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SessionInfo, 0, len(m.mem))
	for id, e := range m.mem {
		out = append(out, SessionInfo{ID: id, Title: e.title, UpdatedAt: e.updatedAt, Pinned: e.pinned})
	}
	sortSessionInfosByRecency(out)
	return out, nil
}

func sortSessionInfosByRecency(infos []SessionInfo) {
	sort.SliceStable(infos, func(i, j int) bool {
		return infos[i].UpdatedAt.After(infos[j].UpdatedAt)
	})
}

// SanitizeFilename replaces every byte outside [A-Za-z0-9-_] with '-'.
func SanitizeFilename(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '-')
		}
	}
	return string(out)
}

type SessionInfo struct {
	ID        string
	Title     string
	UpdatedAt time.Time
	Pinned    bool
}

const (
	sessionIDPrefix    = "ses_"
	sessionIDRandomLen = 16
)

func GenerateSessionID() string {
	return generateSessionIDAt(time.Now())
}

func generateSessionIDAt(now time.Time) string {
	ms := now.UnixMilli()
	enc := encodeBase32Desc(ms)
	rnd := randomBase32(sessionIDRandomLen)
	return sessionIDPrefix + enc + rnd
}

func DefaultSessionTitle(now time.Time) string {
	return fmt.Sprintf("New session - %s", now.UTC().Format(time.RFC3339))
}

func IsDefaultSessionTitle(title string) bool {
	return strings.HasPrefix(title, "New session - ")
}

func DeriveTitle(msgs []Message, now time.Time) string {
	const titleMaxRunes = 60
	for _, m := range msgs {
		if m.Role != RoleUser {
			continue
		}
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[:i]
		}
		return truncateRunes(text, titleMaxRunes)
	}
	return DefaultSessionTitle(now)
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func encodeBase32Desc(v int64) string {
	const width = 10
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		idx := int(v & 0x1f)
		v >>= 5
		out[i] = alphabet[31-idx]
	}
	return string(out)
}

func randomBase32(n int) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	out := make([]byte, n)
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		now := time.Now().UnixNano()
		for i := range out {
			out[i] = alphabet[int(now>>(uint(i)*3))&0x1f]
		}
		return string(out)
	}
	for i := 0; i < n; i++ {
		out[i] = alphabet[int(buf[i])&0x1f]
	}
	return string(out)
}
