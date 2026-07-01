package main

import (
	"testing"

	"github.com/skys-mission/creator-agent/config"
)

func TestResolveVariantConfig_MissingVariant(t *testing.T) {
	prof := config.Profile{
		Model: "test-model",
		Variants: map[string]config.Variant{
			"fast": {},
		},
	}
	if _, err := resolveVariantConfig(prof, 0, "slow"); err == nil {
		t.Fatal("expected error for unknown variant")
	}
}

func TestResolveVariantConfig_CarriedVariantPresent(t *testing.T) {
	prof := config.Profile{
		Model: "test-model",
		Variants: map[string]config.Variant{
			"fast": {MaxTokens: intPtr(1024)},
		},
	}
	pc, err := resolveVariantConfig(prof, 0, "fast")
	if err != nil {
		t.Fatalf("expected variant fast on profile: %v", err)
	}
	if pc.MaxTokens == nil || *pc.MaxTokens != 1024 {
		t.Fatalf("expected max_tokens override from variant, got %+v", pc.MaxTokens)
	}
}

func intPtr(v int) *int { return &v }
