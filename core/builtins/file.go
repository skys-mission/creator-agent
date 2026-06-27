package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/skys-mission/creator-agent/core"
)

const maxReadBytes = 10 << 20 // 10 MiB

type ReadTool struct{}

func NewReadTool() *ReadTool { return &ReadTool{} }

func (ReadTool) Info() core.ToolInfo {
	return core.ToolInfo{
		Name:        "read",
		Description: "Read a file's content from disk. Input: {\"path\": \"<file path>\"}.",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "absolute or relative file path"}
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
		Path string `json:"path"`
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
		if info.Size() > maxReadBytes {
			return core.ToolResult{
				Content: fmt.Sprintf("file %q is too large (%d bytes, max %d); use grep to search or read specific portions", args.Path, info.Size(), maxReadBytes),
				IsError: true,
			}, nil
		}
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return core.ToolResult{Content: fmt.Sprintf("cannot read %q: %v", args.Path, err), IsError: true}, nil
	}
	return core.ToolResult{Content: string(data)}, nil
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
		Description:     "Edit a file by replacing a unique old_string with new_string. Input: {\"path\":\"\",\"old_string\":\"\",\"new_string\":\"\"}. old_string must appear exactly once.",
		InputSchema:     json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"}},"required":["path","old_string","new_string"]}`),
		ReadOnly:        false,
		ConcurrencySafe: false,
	}
}

func (EditTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
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
	if count > 1 {
		return core.ToolResult{Content: fmt.Sprintf("error: old_string matches %d times; include more context to make it unique", count), IsError: true}, nil
	}
	newContent := strings.Replace(content, args.OldString, args.NewString, 1)
	if err := atomicWriteFile(resolved, []byte(newContent), 0o644); err != nil {
		return core.ToolResult{Content: fmt.Sprintf("write: %v", err), IsError: true}, nil
	}
	return core.ToolResult{Content: fmt.Sprintf("edited %s", args.Path)}, nil
}
