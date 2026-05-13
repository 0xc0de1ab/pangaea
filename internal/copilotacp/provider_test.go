package copilotacp

import (
	"strings"
	"testing"

	"github.com/0xc0de1ab/pangaea/internal/compat"
)

func TestFlattenCompatMessages(t *testing.T) {
	got, err := flattenCompatMessages([]compat.Message{{
		Role: compat.MessageRoleUser,
		Content: []compat.ContentPart{{
			Type: compat.ContentPartText,
			Text: "hello",
		}},
	}})
	if err != nil {
		t.Fatalf("flattenCompatMessages: %v", err)
	}
	if !strings.Contains(got, "[user]\nhello") {
		t.Fatalf("flattened prompt = %q", got)
	}
}
