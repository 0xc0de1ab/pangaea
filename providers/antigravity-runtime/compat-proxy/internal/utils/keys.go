package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func generateRandomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "fallback-random-string"
	}
	return hex.EncodeToString(b)
}

// GenerateOpenAIKey creates a key starting with sk-c0de1ab-
func GenerateOpenAIKey() string {
	return fmt.Sprintf("sk-c0de1ab-%s", generateRandomString(16))
}

// GenerateAnthropicKey creates a key starting with ant-api03-c0de1ab-
func GenerateAnthropicKey() string {
	return fmt.Sprintf("ant-api03-c0de1ab-%s", generateRandomString(16))
}

// GenerateGeminiKey creates a key starting with gemini-c0de1ab-
func GenerateGeminiKey() string {
	return fmt.Sprintf("gemini-c0de1ab-%s", generateRandomString(16))
}
