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

type modelsOptions struct {
	Addr   string `flag:"addr" env:"PROXY_ADDR" usage:"Address of the proxy server"`
	APIKey string `flag:"api-key" env:"OPENAI_API_KEY" usage:"API key for authentication"`
}

func newModelsCommand() *cobra.Command {
	opts := &modelsOptions{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String("addr", "http://localhost:8080", "Address of the proxy server").
		String("api-key", "sk-c0de1ab-test", "API key for authentication")

	cmd := &cobra.Command{
		Use:   "models",
		Short: "List available models and their current quota status",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, opts, args...); err != nil {
				return usageError(cmd, err)
			}
			return runModels(opts)
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}

func runModels(opts *modelsOptions) error {
	client := &http.Client{Timeout: 10 * time.Second}

	if !strings.HasPrefix(opts.Addr, "http") {
		opts.Addr = "http://" + opts.Addr
	}

	// 1. Fetch Account Info for header
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

	fmt.Printf("Antigravity Models at %s:\n\n", opts.Addr)

	ids := make([]string, 0, len(modelDetails))
	for id := range modelDetails {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		nameI := modelDetails[ids[i]].Label
		if nameI == "" {
			nameI = ids[i]
		}
		nameJ := modelDetails[ids[j]].Label
		if nameJ == "" {
			nameJ = ids[j]
		}
		if nameI == nameJ {
			return ids[i] < ids[j]
		}
		return nameI < nameJ
	})

	for _, id := range ids {
		m := modelDetails[id]
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

	return nil
}
