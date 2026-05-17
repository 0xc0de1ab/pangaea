package formats

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUsageWindowMarshalKeepsZeroRemainingPct(t *testing.T) {
	data, err := json.Marshal(UsageReport{
		Windows: []UsageWindow{
			{Label: "5h limit", RemainingPct: 0, Unit: "5h window"},
		},
	})
	if err != nil {
		t.Fatalf("marshal usage report: %v", err)
	}
	if !strings.Contains(string(data), `"label":"5h limit"`) {
		t.Fatalf("usage window label was not included: %s", data)
	}
	if !strings.Contains(string(data), `"remaining_pct":0`) {
		t.Fatalf("zero remaining_pct should remain visible: %s", data)
	}
}
