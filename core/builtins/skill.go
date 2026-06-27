package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/skys-mission/creator-agent/core"
)

// SkillTool loads a skill's full body on demand.
//
// Data flow:
//   - The Skills middleware has already injected all skill frontmatter summaries into the system prompt
//     (the model knows which skills are available);
//   - When the model decides to use a skill, it calls this tool (input: {name}), and the body is returned
//     as a tool result into the context;
//   - The body guides the model's subsequent behavior (skill = "injected prompt package").
//
// This keeps the body out of the system prompt, loading it only when needed to prevent context bloat.
type SkillTool struct {
	provider core.SkillProvider
}

// NewSkillTool creates a SkillTool. provider is typically *middlewares.Skills (implements core.SkillProvider).
func NewSkillTool(provider core.SkillProvider) *SkillTool {
	return &SkillTool{provider: provider}
}

func (s *SkillTool) Info() core.ToolInfo {
	return core.ToolInfo{
		Name: "skill",
		Description: `Load a skill's full instructions by name. Use this when you want to follow a skill's detailed guidance (the available skill names and when-to-use hints are listed in the system prompt).

Input:
- name: the skill name (from the skills list in system prompt)

Returns the skill's full markdown body, which you should follow for subsequent actions in this task.`,
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "name": {"type": "string", "description": "skill name from the available-skills list"}
  },
  "required": ["name"]
}`),
		ReadOnly:        true, // skill is an injected prompt package, no side effects
		ConcurrencySafe: true, // pure lookup, no side effects, can run in parallel
		MaxResultChars:  10000,
	}
}

func (s *SkillTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	if s.provider == nil {
		return core.ToolResult{Content: "Error: skills not configured", IsError: true}, nil
	}
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return invalidInputResult(err), nil
	}
	if args.Name == "" {
		return core.ToolResult{Content: "Error: name is required", IsError: true}, nil
	}
	sk, ok := s.provider.LookupSkill(args.Name)
	if !ok {
		return core.ToolResult{Content: fmt.Sprintf("Error: skill %q not found", args.Name), IsError: true}, nil
	}
	if sk.Body == "" {
		return core.ToolResult{Content: fmt.Sprintf("(skill %q has empty body)", args.Name), IsError: false}, nil
	}
	// When the skill declares a tool whitelist, prepend it so the model knows the intended tool scope
	// for following this skill's instructions (mirrors opencode's skill tool-scoping hint).
	content := sk.Body
	if len(sk.Tools) > 0 {
		content = fmt.Sprintf("[Allowed tools for this skill: %s]\n\n%s", strings.Join(sk.Tools, ", "), content)
	}
	return core.ToolResult{Content: content}, nil
}

// Ensure SkillTool implements core.Tool.
var _ core.Tool = (*SkillTool)(nil)
