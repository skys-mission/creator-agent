// Package openaichat implements the OpenAI Chat Completions wire protocol on top of the official
// OpenAI Go SDK (github.com/openai/openai-go). The same client drives any OpenAI-compatible
// endpoint through Config.BaseURL. Provider SDK types stop at this package boundary.
//
// Chain-of-thought handling follows the de-facto ecosystem standard (documented in
// docs/architecture.md §4): inbound reasoning is scanned across the known wire field set
// (reasoning_content / reasoning_details / reasoning / reasoning_text), and assistant thinking is
// echoed into outbound history under the dialect the endpoint itself spoke — learned per
// endpoint from its responses. Inbound and outbound are configured independently
// (Config.ReasoningKeyIn / Config.ReasoningKeyOut pin one side each); DisableThinkingEcho keeps
// thinking out of outbound entirely. Showing thinking to the user is a UI concern (the TUI has
// its own display mode). Deliberately not handled yet: multimodal parts, sampling knobs;
// request-side thinking enablement (reasoning_effort and friends) is dialect-specific and
// carried by Params.
package openaichat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/skys-mission/creator-agent/contract"
)

// defaultBaseURL is the official endpoint. It is pinned explicitly (instead of trusting the SDK
// default) so OPENAI_BASE_URL in the environment can never silently reroute a configured model.
const defaultBaseURL = "https://api.openai.com/v1"

// Config configures one model endpoint speaking this protocol.
type Config struct {
	Name    string // optional display label; falls back to ModelID
	BaseURL string // endpoint root including the API prefix; empty = defaultBaseURL
	ModelID string // required
	APIKey  string // resolved secret; may be empty for endpoints without auth

	// DisableThinkingEcho keeps assistant thinking out of outbound history. The zero value
	// echoes it (the ecosystem default): endpoints that don't understand the field ignore it,
	// while some gateways require thinking in history. Inbound reasoning is always parsed and
	// emitted — whether the user SEES it is the UI's concern, not the wire's.
	DisableThinkingEcho bool
	// ReasoningKeyIn pins the wire field name reasoning content is READ from (non-standard
	// gateways). Empty means smart adaptation: scan the de-facto inbound field set.
	ReasoningKeyIn string
	// ReasoningKeyOut pins the wire field name thinking is echoed INTO on outbound history.
	// Empty means "reply in the dialect the endpoint spoke" (see reasoningDialect). Only
	// meaningful when the echo is enabled.
	ReasoningKeyOut string
	// Reasoning is the thinking-depth control declaration (kind + supported levels + default);
	// see contract.Reasoning. kind=effort maps to reasoning_effort on this protocol.
	Reasoning contract.Reasoning
}

// Client implements adapters.ModelClient for the Chat Completions protocol.
type Client struct {
	cfg     Config
	name    string
	api     openai.Client
	dialect *reasoningDialect // per-endpoint reasoning-field dialect (shared across streams)
}

// New builds a client. The config is authoritative: SDK environment defaults
// (OPENAI_API_KEY / OPENAI_BASE_URL) are explicitly overridden on every call.
func New(cfg Config) (*Client, error) {
	if cfg.ModelID == "" {
		return nil, errors.New("openaichat: ModelID is required")
	}
	cfg.ReasoningKeyIn = strings.TrimSpace(cfg.ReasoningKeyIn)
	cfg.ReasoningKeyOut = strings.TrimSpace(cfg.ReasoningKeyOut)
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	api := openai.NewClient(
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(base),
	)
	name := cfg.Name
	if name == "" {
		name = cfg.ModelID
	}
	return &Client{cfg: cfg, name: name, api: api, dialect: newReasoningDialect(cfg.ReasoningKeyIn, cfg.ReasoningKeyOut)}, nil
}

// echoKey returns the wire key for echoing thinking into outbound history, or "" when the echo
// is disabled. A pinned ReasoningKeyOut wins; otherwise the key is what the endpoint spoke
// (learned), falling back to the pinned inbound key, then the de-facto default.
func (c *Client) echoKey() string {
	if c.cfg.DisableThinkingEcho {
		return ""
	}
	return c.dialect.outboundKey()
}

// Name returns the human-readable model identity for logs and UI.
func (c *Client) Name() string { return c.name }

// Stream runs one model call and streams contract events. See adapters.ModelClient for the event
// stream protocol. Errors known before any output (invalid request, unreachable host, HTTP error
// status) are returned as the error result; failures mid-stream become ErrorEvent + FinishError.
//
// The returned channel must be drained until it closes.
func (c *Client) Stream(ctx context.Context, req *contract.ModelRequest) (<-chan contract.Event, error) {
	params, err := buildParams(c.cfg.ModelID, req, c.echoKey(), c.cfg.Reasoning)
	if err != nil {
		return nil, err
	}
	stream := c.api.Chat.Completions.NewStreaming(ctx, params, reasoningRequestOptions(c.cfg.Reasoning)...)
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("openaichat %s: %w", c.cfg.ModelID, err)
	}

	out := make(chan contract.Event)
	go func() {
		defer close(out)
		defer stream.Close()

		mapper := newStreamMapper(c.dialect)
		for stream.Next() {
			chunk := stream.Current()
			for _, ev := range mapper.Map(&chunk) {
				out <- ev
			}
		}
		switch {
		case ctx.Err() != nil:
			out <- contract.FinishEvent{Reason: contract.FinishCanceled}
		case stream.Err() != nil:
			out <- contract.ErrorEvent{Err: fmt.Errorf("openaichat %s: %w", c.cfg.ModelID, stream.Err())}
			out <- contract.FinishEvent{Reason: contract.FinishError}
		default:
			out <- contract.FinishEvent{Reason: contract.FinishStop}
		}
	}()
	return out, nil
}
