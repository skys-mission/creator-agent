package adapters

import (
	"fmt"

	"github.com/skys-mission/creator-agent/adapters/openaichat"
	"github.com/skys-mission/creator-agent/contract"
)

// New builds the ModelClient for a configured endpoint. One case per protocol; adding a protocol
// means adding its subpackage and one line here.
func New(m contract.Model) (ModelClient, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	switch m.Protocol {
	case contract.ProtocolOpenAIChat:
		return openaichat.New(openaichat.Config{
			Name:                m.Name,
			BaseURL:             m.BaseURL,
			ModelID:             m.ModelID,
			APIKey:              m.APIKey,
			DisableThinkingEcho: m.Params.ThinkingEcho == contract.ThinkingEchoOff,
			ReasoningKeyIn:      m.Params.ReasoningKeyIn,
			ReasoningKeyOut:     m.Params.ReasoningKeyOut,
			Reasoning:           m.Params.Reasoning,
		})
	default:
		return nil, fmt.Errorf("adapters: unknown protocol %q", m.Protocol)
	}
}

// Compile-time conformance of each protocol implementation. Asserted here so subpackages never
// need to import this package (which would be an import cycle).
var _ ModelClient = (*openaichat.Client)(nil)
