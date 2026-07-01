// Package shared holds protocol-agnostic building blocks shared across model adapters
// (openai-chat, openai-responses, anthropic): the unified ProviderConfig contract and an
// HTTP/1.1-forced client. It depends only on the standard library, never on any provider SDK,
// so adapters remain isolated anti-corruption layers.
package shared

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// ProviderConfig is the provider-agnostic configuration consumed by every adapter's constructor.
// Each adapter (openai-chat / openai-responses / anthropic) translates it into its own SDK
// parameters; per-request values in core.ModelRequest always override the variant defaults here.
type ProviderConfig struct {
	BaseURL        string
	APIKey         string
	Model          string
	RequestTimeout time.Duration // total timeout for a single streaming call; 0 = adapter default, negative = unlimited

	// Variant overrides (resolved from the selected profile variant by the caller). Empty/nil = none.
	// Applied on every request; runtime variant switching builds a new ProviderConfig.
	ExtraHeaders map[string]string
	ExtraBody    map[string]any

	// Generation defaults from the variant; nil = no override. Per-request values in ModelRequest
	// always take precedence.
	Temperature *float64
	TopP        *float64
	MaxTokens   *int64
}

// HTTP1Client creates an http.Client forced to HTTP/1.1.
//
// ForceAttemptHTTP2=false plus TLS NextProtos limited to http/1.1 (no ALPN h2 negotiation)
// prevents http2 connection establishment entirely, avoiding the http2Framer.ReadFrame panic path
// observed with some OpenAI-compatible endpoints (the panic happens outside any recover scope).
// Shared by all streaming adapters.
func HTTP1Client(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		ForceAttemptHTTP2: false, // do not attempt http2
		// ALPN only negotiates http/1.1 (even if the server supports h2, the client will not upgrade)
		TLSClientConfig: &tls.Config{
			NextProtos: []string{"http/1.1"},
		},
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	client := &http.Client{Transport: transport}
	if timeout > 0 {
		client.Timeout = timeout
	}
	return client
}
