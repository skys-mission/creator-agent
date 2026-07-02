package builtins

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/skys-mission/creator-agent/core"
)

const (
	maxReadBytes = 10 << 20 // 10 MiB: hard cap for whole-file operations (edit/write comparisons)

	// defaultReadLimit is the number of lines returned when the caller does not set "limit".
	defaultReadLimit = 2000
	// maxLineChars bounds a single emitted line so one pathological long line (e.g. minified JS)
	// cannot blow the result budget; the overflow is dropped with a marker.
	maxLineChars = 2000
	// binaryProbeBytes is how many leading bytes are scanned for a NUL to classify a file as binary.
	binaryProbeBytes = 8192
)

type ReadTool struct{}

func NewReadTool() *ReadTool { return &ReadTool{} }

func (ReadTool) Info() core.ToolInfo {
	return core.ToolInfo{
		Name: "read",
		Description: "Read a file's content from disk with line numbers. By default returns up to " +
			"2000 lines from the start; use offset/limit to page through large files. Binary files are rejected.",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "absolute or relative file path"},
    "offset": {"type": "integer", "description": "1-based line number to start reading from (default 1)"},
    "limit": {"type": "integer", "description": "maximum number of lines to return (default 2000)"}
  },
  "required": ["path"]
}`),
		ReadOnly:        true,
		ConcurrencySafe: true,
		MaxResultChars:  20000,
	}
}

func (ReadTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return invalidInputResult(err), nil
	}
	resolved, err := resolveToolPath(args.Path)
	if err != nil {
		return core.ToolResult{Content: err.Error(), IsError: true}, nil
	}
	if info, err := os.Stat(resolved); err == nil {
		if info.IsDir() {
			return core.ToolResult{
				Content: fmt.Sprintf("%q is a directory, not a file; use glob to list files or read a specific file", args.Path),
				IsError: true,
			}, nil
		}
		if info.Size() > maxReadBytes && args.Offset <= 0 && args.Limit <= 0 {
			return core.ToolResult{
				Content: fmt.Sprintf("file %q is too large (%d bytes, max %d for a full read); pass offset/limit to page through it or use grep to search", args.Path, info.Size(), maxReadBytes),
				IsError: true,
			}, nil
		}
	}

	f, err := os.Open(resolved)
	if err != nil {
		return core.ToolResult{Content: fmt.Sprintf("cannot read %q: %v", args.Path, err), IsError: true}, nil
	}
	defer f.Close()

	if binary, probeErr := isBinaryFile(f); probeErr != nil {
		return core.ToolResult{Content: fmt.Sprintf("cannot read %q: %v", args.Path, probeErr), IsError: true}, nil
	} else if binary {
		return core.ToolResult{
			Content: fmt.Sprintf("%q appears to be a binary file; refusing to read as text", args.Path),
			IsError: true,
		}, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return core.ToolResult{Content: fmt.Sprintf("cannot read %q: %v", args.Path, err), IsError: true}, nil
	}

	offset := args.Offset
	if offset < 1 {
		offset = 1
	}
	limit := args.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}
	return readLines(ctx, f, args.Path, offset, limit)
}

// isBinaryFile reports whether the first binaryProbeBytes of r contain a NUL byte, the standard
// heuristic for distinguishing binary from text. It reads from the current position and leaves the
// offset advanced; callers seek back to 0 before consuming content.
func isBinaryFile(r io.Reader) (bool, error) {
	buf := make([]byte, binaryProbeBytes)
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	return bytes.IndexByte(buf[:n], 0) >= 0, nil
}

// readLines emits [offset, offset+limit) as "<lineNo>\t<content>" (cat -n style) so the model can
// cite exact lines and page deterministically. It streams line-by-line (no whole-file buffering),
// truncates any single line beyond maxLineChars, honors ctx cancellation, and appends a hint when
// more lines remain past the window.
func readLines(ctx context.Context, f *os.File, path string, offset, limit int) (core.ToolResult, error) {
	br := bufio.NewReader(f)
	var b strings.Builder
	lineNo := 0
	emitted := 0
	hasMore := false
	for {
		if err := ctx.Err(); err != nil {
			return core.ToolResult{}, err
		}
		line, readErr := br.ReadString('\n')
		if len(line) > 0 {
			lineNo++
			if lineNo >= offset {
				if emitted >= limit {
					hasMore = true
					break
				}
				content := strings.TrimSuffix(line, "\n")
				content = strings.TrimSuffix(content, "\r")
				if len(content) > maxLineChars {
					content = content[:maxLineChars] + fmt.Sprintf("... [line truncated, %d more chars]", len(content)-maxLineChars)
				}
				fmt.Fprintf(&b, "%d\t%s\n", lineNo, content)
				emitted++
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return core.ToolResult{Content: fmt.Sprintf("cannot read %q: %v", path, readErr), IsError: true}, nil
		}
	}

	if emitted == 0 {
		if lineNo == 0 {
			return core.ToolResult{Content: fmt.Sprintf("%q is empty", path)}, nil
		}
		return core.ToolResult{
			Content: fmt.Sprintf("offset %d is past end of file (%d lines total)", offset, lineNo),
			IsError: true,
		}, nil
	}
	if hasMore {
		fmt.Fprintf(&b, "\n[truncated: showed lines %d-%d; pass offset=%d to continue]", offset, offset+emitted-1, offset+emitted)
	}
	return core.ToolResult{Content: b.String()}, nil
}

type WriteTool struct{}

func NewWriteTool() *WriteTool { return &WriteTool{} }

func (WriteTool) Info() core.ToolInfo {
	return core.ToolInfo{
		Name:            "write",
		Description:     "Create or overwrite a file with the given content. Input: {\"path\": \"\", \"content\": \"\"}.",
		InputSchema:     json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
		ReadOnly:        false,
		ConcurrencySafe: false,
	}
}

func (WriteTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return invalidInputResult(err), nil
	}
	resolved, err := resolveToolPath(args.Path)
	if err != nil {
		return core.ToolResult{Content: err.Error(), IsError: true}, nil
	}
	if info, err := os.Stat(resolved); err == nil && info.IsDir() {
		return core.ToolResult{
			Content: fmt.Sprintf("%q is a directory; write needs a file path", args.Path),
			IsError: true,
		}, nil
	}
	if err := atomicWriteFile(resolved, []byte(args.Content), 0o644); err != nil {
		return core.ToolResult{Content: fmt.Sprintf("write %q: %v", args.Path, err), IsError: true}, nil
	}
	return core.ToolResult{Content: fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path)}, nil
}

type EditTool struct{}

func NewEditTool() *EditTool { return &EditTool{} }

func (EditTool) Info() core.ToolInfo {
	return core.ToolInfo{
		Name:            "edit",
		Description:     "Edit a file by replacing old_string with new_string. By default old_string must appear exactly once; set replace_all=true to replace every occurrence. Input: {\"path\":\"\",\"old_string\":\"\",\"new_string\":\"\",\"replace_all\":false}.",
		InputSchema:     json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean","description":"replace all occurrences instead of requiring a unique match (default false)"}},"required":["path","old_string","new_string"]}`),
		ReadOnly:        false,
		ConcurrencySafe: false,
	}
}

func (EditTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return invalidInputResult(err), nil
	}
	resolved, err := resolveToolPath(args.Path)
	if err != nil {
		return core.ToolResult{Content: err.Error(), IsError: true}, nil
	}
	if args.OldString == "" {
		return core.ToolResult{Content: "error: old_string is empty; provide the exact text to replace", IsError: true}, nil
	}
	if args.OldString == args.NewString {
		return core.ToolResult{Content: "error: old_string and new_string are identical; nothing to change", IsError: true}, nil
	}
	if info, err := os.Stat(resolved); err == nil && info.Size() > maxReadBytes {
		return core.ToolResult{
			Content: fmt.Sprintf("file %q is too large (%d bytes, max %d) to edit", args.Path, info.Size(), maxReadBytes),
			IsError: true,
		}, nil
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return core.ToolResult{Content: fmt.Sprintf("read %q: %v", args.Path, err), IsError: true}, nil
	}
	content := string(data)
	count := strings.Count(content, args.OldString)
	if count == 0 {
		return core.ToolResult{Content: "error: old_string not found in file", IsError: true}, nil
	}
	if count > 1 && !args.ReplaceAll {
		return core.ToolResult{Content: fmt.Sprintf("error: old_string matches %d times; include more context to make it unique or set replace_all=true", count), IsError: true}, nil
	}
	newContent := strings.ReplaceAll(content, args.OldString, args.NewString)
	if err := atomicWriteFile(resolved, []byte(newContent), 0o644); err != nil {
		return core.ToolResult{Content: fmt.Sprintf("write: %v", err), IsError: true}, nil
	}
	if count > 1 {
		return core.ToolResult{Content: fmt.Sprintf("edited %s (%d replacements)", args.Path, count)}, nil
	}
	return core.ToolResult{Content: fmt.Sprintf("edited %s", args.Path)}, nil
}
