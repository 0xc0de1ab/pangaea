package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/google/antigravity-compat-proxy/internal/models"
	"github.com/spf13/cobra"
)

type accountOptions struct {
	Addr   string `flag:"addr" env:"PROXY_ADDR" usage:"Address of the proxy server"`
	APIKey string `flag:"api-key" env:"OPENAI_API_KEY" usage:"API key for authentication"`
}

func newAccountCommand() *cobra.Command {
	opts := &accountOptions{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String("addr", "http://localhost:8080", "Address of the proxy server").
		String("api-key", "sk-c0de1ab-test", "API key for authentication")

	cmd := &cobra.Command{
		Use:   "account",
		Short: "Display Antigravity account and plan information",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, opts, args...); err != nil {
				return usageError(cmd, err)
			}
			return runAccount(opts)
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}

func runAccount(opts *accountOptions) error {
	client := &http.Client{Timeout: 10 * time.Second}

	if !strings.HasPrefix(opts.Addr, "http") {
		opts.Addr = "http://" + opts.Addr
	}

	fmt.Printf("Connecting to Antigravity Proxy at %s...\n\n", opts.Addr)

	req, _ := http.NewRequest("GET", opts.Addr+"/v1/account", nil)
	req.Header.Set("Authorization", "Bearer "+opts.APIKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch account info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("proxy returned status %d", resp.StatusCode)
	}

	var acc models.UserStatus
	if err := json.NewDecoder(resp.Body).Decode(&acc); err != nil {
		return fmt.Errorf("failed to decode account info: %w", err)
	}

	fmt.Println("--- Antigravity Account Information ---")
	fmt.Printf("Name:      %s\n", acc.Name)
	fmt.Printf("Email:     %s\n", acc.Email)
	fmt.Println()

	if acc.UserTier != nil && strings.TrimSpace(acc.UserTier.Name) != "" {
		fmt.Printf("Your Plan:    %s\n", acc.UserTier.Name)
		if acc.UserTier.UpgradeSubscriptionText != "" {
			fmt.Printf("Status:       %s\n", acc.UserTier.UpgradeSubscriptionText)
		}
	} else if acc.PlanStatus != nil && acc.PlanStatus.PlanInfo != nil {
		fmt.Printf("Your Plan:    %s\n", acc.PlanStatus.PlanInfo.PlanName)
	}

	fmt.Println("---------------------------------------")

	return nil
}
