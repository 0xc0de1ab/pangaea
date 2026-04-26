package notifier

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

type periodicState struct {
	mu   sync.Mutex
	last map[string]string
}

func (s *periodicState) unchanged(dest, body string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.last = make(map[string]string)
	}
	return s.last[dest] == body
}

func (s *periodicState) remember(dest, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.last = make(map[string]string)
	}
	s.last[dest] = body
}

type periodicDigestRecord struct {
	Profile     string                `json:"profile"`
	NoTruth     bool                  `json:"no_truth,omitempty"`
	AccountID   string                `json:"account_id"`
	Fingerprint string                `json:"fingerprint"`
	Nodes       []string              `json:"nodes,omitempty"`
	ValidUntil  string                `json:"valid_until,omitempty"`
	LastRefresh string                `json:"last_refresh,omitempty"`
	Usage       []periodicDigestUsage `json:"usage,omitempty"`
}

type periodicDigestUsage struct {
	Label        string `json:"label,omitempty"`
	RemainingPct string `json:"remaining_pct,omitempty"`
	ResetAt      string `json:"reset_at,omitempty"`
}

func periodicDigest(records []ReportRecord) string {
	items := make([]periodicDigestRecord, 0, len(records))
	for _, record := range records {
		sum := parseSummary(record.Truth.Summary)
		accountID := sum.Extra["account_id"]
		if accountID == "" {
			accountID = record.Truth.Account
		}
		if accountID == "" {
			accountID = sum.Identity
		}
		var validUntil string
		if !sum.ExpiresAt.IsZero() {
			validUntil = sum.ExpiresAt.UTC().Format(time.RFC3339Nano)
		}
		var lastRefresh string
		if ts, ok := parseSummaryTime(sum.Extra["last_refresh"]); ok {
			lastRefresh = ts.UTC().Format(time.RFC3339Nano)
		}
		usage := make([]periodicDigestUsage, 0, len(record.Usage.Windows))
		for _, w := range record.Usage.Windows {
			item := periodicDigestUsage{
				Label:        w.Label,
				RemainingPct: fmt.Sprintf("%.3f", w.RemainingPct),
			}
			if !w.ResetAt.IsZero() {
				item.ResetAt = w.ResetAt.UTC().Format(time.RFC3339Nano)
			}
			usage = append(usage, item)
		}
		items = append(items, periodicDigestRecord{
			Profile:     record.Truth.Profile,
			NoTruth:     record.Truth.NoTruth,
			AccountID:   accountID,
			Fingerprint: record.Truth.Fingerprint,
			Nodes:       normalizedNodes(record.Truth),
			ValidUntil:  validUntil,
			LastRefresh: lastRefresh,
			Usage:       usage,
		})
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	return string(raw)
}

func groupPeriodicRecords(records []ReportRecord, routeFor func(profile, account string) string) map[string][]ReportRecord {
	out := make(map[string][]ReportRecord)
	for _, r := range records {
		dest := routeFor(r.Truth.Profile, r.Truth.Account)
		if dest == "" {
			continue
		}
		out[dest] = append(out[dest], r)
	}
	for dest := range out {
		sort.Slice(out[dest], func(i, j int) bool {
			a := out[dest][i].Truth
			b := out[dest][j].Truth
			if a.Profile != b.Profile {
				return a.Profile < b.Profile
			}
			if a.Account != b.Account {
				return a.Account < b.Account
			}
			return a.Fingerprint < b.Fingerprint
		})
	}
	return out
}

func groupSessionEvents(events []TruthRecord, routeFor func(profile, account string) string) map[string][]TruthRecord {
	out := make(map[string][]TruthRecord)
	for _, event := range events {
		dest := routeFor(event.Profile, event.Account)
		if dest == "" {
			continue
		}
		out[dest] = append(out[dest], event)
	}
	for dest := range out {
		sort.Slice(out[dest], func(i, j int) bool {
			a := out[dest][i]
			b := out[dest][j]
			if a.Profile != b.Profile {
				return a.Profile < b.Profile
			}
			return a.NodeID < b.NodeID
		})
	}
	return out
}

func sortedGroupKeys(groups map[string][]ReportRecord) []string {
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedTruthGroupKeys(groups map[string][]TruthRecord) []string {
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
