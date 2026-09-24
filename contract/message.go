package contract

import "encoding/json"

// Role is the message role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is the core message type.
//
// Anti-corruption decision: the core defines its own Message, never reusing an underlying SDK
// schema. Bidirectional conversion with a provider SDK is the adapter layer's job, not this type's.
type Message struct {
	Role           Role
	Content        string         // text content
	Parts          []Part         // multimodal (images/files), optional
	ToolCalls      []ToolCall     // assistant-initiated tool calls
	ToolCallID     string         // for role=tool, the associated ToolCall.ID
	ToolName       string         // for role=tool, the tool name
	ToolIsError    bool           // for role=tool: whether the tool result was an error (Anthropic tool_result.is_error)
	Reasoning      string         // thinking/reasoning content
	ReasoningToken string         // opaque token to replay reasoning next turn (Anthropic thinking signature; OpenAI Responses encrypted_content)
	Usage          *Usage         // token usage (usually only on assistant messages)
	Extra          map[string]any // extension fields (e.g., cache_control), consumed by adapters
}

// ToolCall is a single tool call initiated by the assistant.
type ToolCall struct {
	ID    string // model-assigned call ID
	Name  string
	Input json.RawMessage // model-generated arguments (JSON), decoded by the tool itself
}

// Usage is the token usage for a single model call.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
}

// Part is a multimodal content fragment.
//
// Kept as a structural placeholder; concrete handling for images/files is refined in later steps.
type Part struct {
	Type     string // "image" / "file" / ...
	MimeType string
	Data     []byte // raw bytes (mutually exclusive with URL)
	URL      string
}

// --- Convenience constructors ---

// SystemMessage constructs a system message.
func SystemMessage(content string) Message {
	return Message{Role: RoleSystem, Content: content}
}

// UserMessage constructs a user message (optionally with multimodal parts).
func UserMessage(content string, parts ...Part) Message {
	return Message{Role: RoleUser, Content: content, Parts: parts}
}

// AssistantMessage constructs an assistant message (optionally with tool calls).
func AssistantMessage(content string, toolCalls ...ToolCall) Message {
	return Message{Role: RoleAssistant, Content: content, ToolCalls: toolCalls}
}

// AssistantMessageWithReasoning constructs an assistant message carrying thinking text and a
// provider-specific replay token, so multi-turn continuity (e.g. Anthropic extended thinking
// signature) survives across turns. The loop uses this to persist every assistant turn.
func AssistantMessageWithReasoning(content, reasoning, reasoningToken string, toolCalls ...ToolCall) Message {
	return Message{
		Role:           RoleAssistant,
		Content:        content,
		Reasoning:      reasoning,
		ReasoningToken: reasoningToken,
		ToolCalls:      toolCalls,
	}
}

// ToolMessage constructs a tool result message (linked to a ToolCall).
func ToolMessage(content, toolCallID, toolName string) Message {
	return Message{Role: RoleTool, Content: content, ToolCallID: toolCallID, ToolName: toolName}
}
