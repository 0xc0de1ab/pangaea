package providerfactory

import (
	"strings"
	"testing"

	"github.com/0xc0de1ab/pangaea/internal/provider"
)

func TestRegistryRejectsDuplicateDefinitions(t *testing.T) {
	_, err := NewRegistry(
		Definition{Service: provider.ServiceCodex},
		Definition{Service: provider.ServiceCodex},
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate provider definition") {
		t.Fatalf("expected duplicate definition error, got %v", err)
	}
}

func TestDefaultRegistryHasProviderDefinitions(t *testing.T) {
	registry := DefaultRegistry()
	for _, service := range []provider.Service{
		provider.ServiceCodex,
		provider.ServiceClaude,
		provider.ServiceGemini,
		provider.ServiceAntigravity,
		provider.ServiceGitHubCopilot,
	} {
		if _, ok := registry.Definition(service); !ok {
			t.Fatalf("default registry missing %s definition", service)
		}
	}
}
