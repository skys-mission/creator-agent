package builtins

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/skys-mission/creator-agent/core"
)

const (
	hardMaxResultChars = 2 << 20 // 2 MiB
	hardMaxMatches     = 10000
	hardMaxPaths       = 10000
	hardMaxWalkDepth   = 50
	hardMaxTaskDepth   = 10
)

func clampInt(v, hi int) int {
	if v > hi {
		return hi
	}
	return v
}

func invalidInputResult(err error) core.ToolResult {
	return core.ToolResult{Content: fmt.Sprintf("invalid input: %v", err), IsError: true}
}

func evalPath(p string) string {
	if ep, err := filepath.EvalSymlinks(p); err == nil {
		return ep
	}
	dir := filepath.Dir(p)
	base := filepath.Base(p)
	if ed, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(ed, base)
	}
	return p
}

func resolveToolPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot determine working directory: %w", err)
	}
	cwd = filepath.Clean(cwd)

	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs = filepath.Clean(filepath.Join(cwd, path))
	}

	cwd = evalPath(cwd)
	abs = evalPath(abs)

	if abs != cwd && !strings.HasPrefix(abs, cwd+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the working directory %q", path, cwd)
	}
	return abs, nil
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink %q", path)
		}
		perm = info.Mode()
	}
	tmp, err := os.CreateTemp(dir, ".tmp-write-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

func walkDepth(path, root string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

func ignoreSetOf(dirs []string) map[string]struct{} {
	set := make(map[string]struct{}, len(dirs))
	for _, d := range dirs {
		set[d] = struct{}{}
	}
	return set
}

func shouldSkipByIgnore(path string, ignoreSet map[string]struct{}) bool {
	for _, seg := range strings.Split(path, string(filepath.Separator)) {
		if _, skip := ignoreSet[seg]; skip {
			return true
		}
	}
	return false
}

func normalizeSettings(def, s core.ToolSettings, useMatches bool) core.ToolSettings {
	if s.MaxResultChars <= 0 {
		s.MaxResultChars = def.MaxResultChars
	}
	if useMatches && s.MaxMatches <= 0 {
		s.MaxMatches = def.MaxMatches
	}
	if s.MaxDepth <= 0 {
		s.MaxDepth = def.MaxDepth
	}
	if len(s.IgnoreDirs) == 0 {
		s.IgnoreDirs = append([]string(nil), def.IgnoreDirs...)
	}
	s.MaxResultChars = clampInt(s.MaxResultChars, hardMaxResultChars)
	if useMatches {
		s.MaxMatches = clampInt(s.MaxMatches, hardMaxMatches)
	}
	s.MaxDepth = clampInt(s.MaxDepth, hardMaxWalkDepth)
	return s
}

type limitedWriter struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.truncated {
		return len(p), nil
	}
	room := w.max - w.buf.Len()
	if room <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if len(p) <= room {
		_, _ = w.buf.Write(p)
		return len(p), nil
	}
	w.buf.Write(p[:room])
	w.truncated = true
	return len(p), nil
}

func (w *limitedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.truncated {
		return fmt.Sprintf("%s\n... (output truncated at %d bytes)", w.buf.String(), w.max)
	}
	return w.buf.String()
}
