package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/google/antigravity-compat-proxy/internal/models"
	"github.com/spf13/cobra"
)

type monitorOptions struct {
	Addr    string `flag:"addr" env:"PROXY_ADDR" usage:"Address of the proxy server"`
	APIKey  string `flag:"api-key" env:"OPENAI_API_KEY" usage:"API key for authentication"`
	Watch   bool   `flag:"watch" usage:"Watch and refresh output periodically"`
	Refresh int    `flag:"refresh" usage:"Refresh interval in seconds"`
}

func newMonitorCommand() *cobra.Command {
	opts := &monitorOptions{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String("addr", "http://localhost:8080", "Address of the proxy server").
		String("api-key", "sk-c0de1ab-test", "API key for authentication").
		Bool("watch", false, "Watch and refresh output periodically").
		Int("refresh", 2, "Refresh interval in seconds")

	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Professional API monitor for models and usage",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, opts, args...); err != nil {
				return usageError(cmd, err)
			}
			if opts.Watch {
				for {
					fmt.Print("\033[H\033[2J") // Clear screen
					if err := runMonitor(opts); err != nil {
						fmt.Printf("Monitor error: %v\n", err)
					}
					time.Sleep(time.Duration(opts.Refresh) * time.Second)
				}
			}
			return runMonitor(opts)
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}

func runMonitor(opts *monitorOptions) error {
	client := &http.Client{Timeout: 10 * time.Second}

	if !strings.HasPrefix(opts.Addr, "http") {
		opts.Addr = "http://" + opts.Addr
	}

	fmt.Printf("Connecting to Antigravity Proxy at %s...\n\n", opts.Addr)

	// 1. Fetch Account Info
	accReq, _ := http.NewRequest("GET", opts.Addr+"/v1/account", nil)
	accReq.Header.Set("Authorization", "Bearer "+opts.APIKey)
	var account *models.UserStatus
	if respAcc, err := client.Do(accReq); err == nil && respAcc.StatusCode == http.StatusOK {
		defer respAcc.Body.Close()
		json.NewDecoder(respAcc.Body).Decode(&account)
	}

	// 2. Fetch Detailed Models
	req, _ := http.NewRequest("GET", opts.Addr+"/v1/models/status", nil)
	req.Header.Set("Authorization", "Bearer "+opts.APIKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch model status: %w", err)
	}
	defer resp.Body.Close()

	var modelDetails map[string]models.ModelDetail
	if err := json.NewDecoder(resp.Body).Decode(&modelDetails); err != nil {
		return fmt.Errorf("failed to decode model status: %w", err)
	}

	// Print Header
	if account != nil {
		fmt.Printf("Account: %s <%s> | Plan: %s\n", account.Name, account.Email, account.PlanStatus.PlanInfo.PlanName)
		if account.UserTier != nil {
			fmt.Printf("Tier:    %s (%s)\n", account.UserTier.Name, account.UserTier.UpgradeSubscriptionText)
		}
		fmt.Println(strings.Repeat("-", 60))
	}

	printDetailedDashboard(modelDetails)

	return nil
}

func printDetailedDashboard(details map[string]models.ModelDetail) {
	fmt.Println("--- Antigravity Model Quota Dashboard ---")
	fmt.Println()

	ids := make([]string, 0, len(details))
	for id := range details {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		nameI := details[ids[i]].Label
		if nameI == "" {
			nameI = ids[i]
		}
		nameJ := details[ids[j]].Label
		if nameJ == "" {
			nameJ = ids[j]
		}
		if nameI == nameJ {
			return ids[i] < ids[j]
		}
		return nameI < nameJ
	})

	for _, id := range ids {
		m := details[id]
		label := m.Label

		displayName := id
		if label != "" && label != id {
			displayName = fmt.Sprintf("%s (%s)", label, id)
		}

		fmt.Printf("\033[1m%s\033[0m\n", displayName)

		if m.QuotaInfo != nil {
			if m.QuotaInfo.ResetTime != "" {
				t, err := time.Parse(time.RFC3339, m.QuotaInfo.ResetTime)
				if err == nil {
					fmt.Printf("  Refreshes in %s\n", formatRelativeTime(t))
				}
			}
			pct := int(m.QuotaInfo.RemainingFraction * 100)
			fmt.Printf("  Usage: %s %d%%\n", renderProgressBar(pct, 40), pct)
		} else {
			fmt.Println("  (No quota information available)")
		}
		fmt.Println()
	}
}

func formatRelativeTime(target time.Time) string {
	diff := time.Until(target)
	if diff < 0 {
		return "now"
	}
	hours := int(diff.Hours())
	minutes := int(diff.Minutes()) % 60
	seconds := int(diff.Seconds()) % 60
	if hours > 0 {
		if minutes > 0 {
			return fmt.Sprintf("%d hours, %d minutes", hours, minutes)
		}
		return fmt.Sprintf("%d hours", hours)
	}
	if minutes > 0 {
		if seconds > 0 {
			return fmt.Sprintf("%d minutes, %d seconds", minutes, seconds)
		}
		return fmt.Sprintf("%d minutes", minutes)
	}
	return fmt.Sprintf("%d seconds", seconds)
}

func renderProgressBar(pct int, width int) string {
	filled := (pct * width) / 100
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	color := "\033[32m" // Green
	if pct < 20 {
		color = "\033[31m" // Red
	} else if pct < 50 {
		color = "\033[33m" // Yellow
	}
	return "[" + color + strings.Repeat("=", filled) + "\033[0m" + strings.Repeat(" ", width-filled) + "]"
}
