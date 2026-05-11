package main

import (
	"fmt"

	"github.com/google/antigravity-compat-proxy/internal/utils"
	"github.com/spf13/cobra"
)

var generateKeysCmd = &cobra.Command{
	Use:   "generate-keys",
	Short: "Generate random API keys for the proxy",
	Long:  `Generates random, provider-compatible API keys containing the 'c0de1ab' proxy identifier.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("--- Antigravity Proxy API Keys ---")
		fmt.Printf("OpenAI:    %s\n", utils.GenerateOpenAIKey())
		fmt.Printf("Anthropic: %s\n", utils.GenerateAnthropicKey())
		fmt.Printf("Gemini:    %s\n", utils.GenerateGeminiKey())
		fmt.Println("----------------------------------")
		fmt.Println("You can use these keys in your SDK configurations or pass them via flags/config.")
	},
}

func init() {
	rootCmd.AddCommand(generateKeysCmd)
}
