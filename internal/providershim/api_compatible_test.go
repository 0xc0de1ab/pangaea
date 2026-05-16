package providershim

import (
	"strings"
	"testing"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestMergeDiscoveredModelPreservesProviderMetadata(t *testing.T) {
	current := provider.Model{
		ID:           "auto",
		Aliases:      []string{"Auto"},
		Capabilities: []provider.Capability{provider.CapabilityOpenAIChat},
	}
	discovered := provider.Model{
		ID:               "auto",
		Kind:             "group",
		GroupMembers:     []string{"gpt-5", "claude-sonnet-4.5"},
		MaxOutputTokens:  4096,
		ContextTokens:    200000,
		MaxContextTokens: 200000,
	}

	got := mergeDiscoveredModel(current, discovered)
	if got.Kind != "group" {
		t.Fatalf("kind = %q", got.Kind)
	}
	if strings.Join(got.GroupMembers, ",") != "gpt-5,claude-sonnet-4.5" {
		t.Fatalf("group members = %#v", got.GroupMembers)
	}
	if got.MaxOutputTokens != 4096 || got.MaxContextTokens != 200000 {
		t.Fatalf("token metadata = output %d context %d", got.MaxOutputTokens, got.MaxContextTokens)
	}
}
