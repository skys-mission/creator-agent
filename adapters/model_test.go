package adapters

import (
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/contract"
)

func TestNewFactory(t *testing.T) {
	c, err := New(contract.Model{Protocol: contract.ProtocolOpenAIChat, ModelID: "m1", BaseURL: "https://example.com/v1"})
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}
	if got := c.Name(); got != "m1" {
		t.Fatalf("Name() = %q, want ModelID fallback", got)
	}

	c2, err := New(contract.Model{Name: "label", Protocol: contract.ProtocolOpenAIChat, ModelID: "m1"})
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}
	if got := c2.Name(); got != "label" {
		t.Fatalf("Name() = %q, want %q", got, "label")
	}

	if _, err := New(contract.Model{Protocol: "no-such-protocol", ModelID: "m1"}); err == nil ||
		!strings.Contains(err.Error(), "unknown protocol") {
		t.Fatalf("New() with unknown protocol = %v, want unknown protocol error", err)
	}

	if _, err := New(contract.Model{Protocol: contract.ProtocolOpenAIChat}); err == nil {
		t.Fatal("New() with missing ModelID = nil, want validation error")
	}
}
