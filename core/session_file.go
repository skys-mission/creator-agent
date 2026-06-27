package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/skys-mission/creator-agent/paths"
)

type sessionFile struct {
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
	Pinned    bool      `json:"pinned,omitempty"`
	Messages  []Message `json:"messages"`
}

// JSONFileStore persists each session as dir/<id>.json (survives restart).
type JSONFileStore struct {
	Dir string

	mu sync.Mutex
}

func NewJSONFileStore(dir string) (*JSONFileStore, error) {
	if dir == "" {
		d, err := paths.SessionsDir()
		if err != nil {
			return nil, fmt.Errorf("resolve session dir: %w", err)
		}
		dir = d
	}
	return &JSONFileStore{Dir: dir}, nil
}

func (s *JSONFileStore) Load(id string) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.safePath(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session %q: %w", id, err)
	}
	msgs, perr := parseSessionData(data)
	if perr != nil {
		backup := path + ".corrupt"
		if renameErr := os.Rename(path, backup); renameErr != nil {
			_ = os.Remove(path)
		}
		Warnf("session %q corrupt (unparseable JSON), backed up to %s; starting fresh", id, backup)
		return nil, nil
	}
	return msgs, nil
}

func parseSessionData(data []byte) ([]Message, error) {
	first := firstJSONByte(data)
	if first == '[' {
		var msgs []Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			return nil, err
		}
		return msgs, nil
	}
	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, err
	}
	return sf.Messages, nil
}

func firstJSONByte(data []byte) byte {
	for _, b := range data {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return b
		}
	}
	return 0
}

func decodeLegacyArray(data []byte, path string) (msgs []Message, title string, updatedAt time.Time, err error) {
	if err = json.Unmarshal(data, &msgs); err != nil {
		return nil, "", time.Time{}, err
	}
	if fi, _ := os.Stat(path); fi != nil {
		updatedAt = fi.ModTime()
	}
	return msgs, DeriveTitle(msgs, updatedAt), updatedAt, nil
}

func (s *JSONFileStore) LoadMeta(id string) (title string, updatedAt time.Time, pinned bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, perr := s.safePath(id)
	if perr != nil {
		return "", time.Time{}, false, perr
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return "", time.Time{}, false, nil
		}
		return "", time.Time{}, false, fmt.Errorf("read session meta %q: %w", id, rerr)
	}
	if firstJSONByte(data) == '[' {
		_, title, t, _ := decodeLegacyArray(data, path)
		return title, t, false, nil
	}
	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return "", time.Time{}, false, nil
	}
	if sf.Title == "" {
		sf.Title = DeriveTitle(sf.Messages, sf.UpdatedAt)
	}
	return sf.Title, sf.UpdatedAt, sf.Pinned, nil
}

func (s *JSONFileStore) Save(id string, msgs []Message) error {
	return s.SaveWithMeta(id, "", msgs)
}

func (s *JSONFileStore) SaveWithMeta(id, title string, msgs []Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("mkdir session dir: %w", err)
	}
	unlock, err := flockFile(filepath.Join(s.Dir, ".lock"))
	if err != nil {
		return fmt.Errorf("acquire session lock: %w", err)
	}
	defer unlock()

	path, err := s.safePath(id)
	if err != nil {
		return err
	}
	now := time.Now()
	t := title
	existingTitle, _, existingPinned, metaErr := s.readMetaLocked(path)
	if t == "" {
		if metaErr == nil && existingTitle != "" {
			t = existingTitle
		} else {
			t = DeriveTitle(msgs, now)
		}
	}
	pinned := existingPinned
	if metaErr != nil {
		pinned = false
	}
	sf := sessionFile{Title: t, UpdatedAt: now, Pinned: pinned, Messages: msgs}
	data, err := json.Marshal(sf)
	if err != nil {
		return fmt.Errorf("encode session %q: %w", id, err)
	}
	if err := atomicWrite(s.Dir, path, data); err != nil {
		return err
	}
	if metaErr != nil {
		return fmt.Errorf("session saved but existing metadata could not be read (title/pin may be derived defaults): %w", metaErr)
	}
	return nil
}

func (s *JSONFileStore) readMetaLocked(path string) (title string, updatedAt time.Time, pinned bool, err error) {
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return "", time.Time{}, false, nil
		}
		return "", time.Time{}, false, rerr
	}
	if firstJSONByte(data) == '[' {
		return "", time.Time{}, false, nil
	}
	var sf sessionFile
	if jerr := json.Unmarshal(data, &sf); jerr != nil {
		return "", time.Time{}, false, nil
	}
	return sf.Title, sf.UpdatedAt, sf.Pinned, nil
}

func (s *JSONFileStore) Clear(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.safePath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear session %q: %w", id, err)
	}
	return nil
}

func (s *JSONFileStore) SetPinned(id string, pinned bool) error {
	return s.rewriteMeta(id, func(sf *sessionFile) { sf.Pinned = pinned })
}

func (s *JSONFileStore) Rename(id, title string) error {
	if title == "" {
		return nil
	}
	return s.rewriteMeta(id, func(sf *sessionFile) { sf.Title = title })
}

func (s *JSONFileStore) rewriteMeta(id string, mutate func(*sessionFile)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("mkdir session dir: %w", err)
	}
	unlock, err := flockFile(filepath.Join(s.Dir, ".lock"))
	if err != nil {
		return fmt.Errorf("acquire session lock: %w", err)
	}
	defer unlock()

	path, err := s.safePath(id)
	if err != nil {
		return err
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return nil
		}
		return fmt.Errorf("read session %q for meta update: %w", id, rerr)
	}
	var sf sessionFile
	if firstJSONByte(data) == '[' {
		msgs, title, mt, derr := decodeLegacyArray(data, path)
		if derr != nil {
			return fmt.Errorf("decode legacy session %q for meta update: %w", id, derr)
		}
		sf = sessionFile{Title: title, UpdatedAt: mt, Pinned: false, Messages: msgs}
	} else {
		if jerr := json.Unmarshal(data, &sf); jerr != nil {
			return fmt.Errorf("decode session %q for meta update: %w", id, jerr)
		}
	}
	mutate(&sf)
	out, merr := json.Marshal(sf)
	if merr != nil {
		return fmt.Errorf("encode session %q: %w", id, merr)
	}
	if err := atomicWrite(s.Dir, path, out); err != nil {
		return err
	}
	return nil
}

func (s *JSONFileStore) List() ([]SessionInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session dir %q: %w", s.Dir, err)
	}
	out := make([]SessionInfo, 0, len(entries))
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		path := filepath.Join(s.Dir, name)
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			continue
		}
		title, updatedAt, pinned, ok := listEntryMeta(data, path)
		if !ok {
			continue
		}
		out = append(out, SessionInfo{ID: id, Title: title, UpdatedAt: updatedAt, Pinned: pinned})
	}
	sortSessionInfosByRecency(out)
	return out, nil
}

func listEntryMeta(data []byte, path string) (title string, updatedAt time.Time, pinned bool, ok bool) {
	if firstJSONByte(data) == '[' {
		_, title, t, err := decodeLegacyArray(data, path)
		return title, t, false, err == nil
	}
	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return "", time.Time{}, false, false
	}
	if sf.Title == "" {
		sf.Title = DeriveTitle(sf.Messages, sf.UpdatedAt)
	}
	if sf.UpdatedAt.IsZero() {
		if fi, _ := os.Stat(path); fi != nil {
			sf.UpdatedAt = fi.ModTime()
		}
	}
	return sf.Title, sf.UpdatedAt, sf.Pinned, true
}

func (s *JSONFileStore) safePath(id string) (string, error) {
	safe := SanitizeFilename(id)
	if safe == "" {
		return "", fmt.Errorf("invalid session id %q", id)
	}
	return filepath.Join(s.Dir, safe+".json"), nil
}

func atomicWrite(dir, dst string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-session-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("rename session: %w", err)
	}
	return nil
}
