package models

// Gemini Error Response
type GeminiErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

type GeminiRequest struct {
	Contents         []GeminiContent   `json:"contents"`
	SystemInstruction *GeminiContent   `json:"system_instruction,omitempty"`
	GenerationConfig *GeminiGenConfig `json:"generation_config,omitempty"`
}

type GeminiContent struct {
	Role  string       `json:"role,omitempty"` // "user", "model"
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text       string      `json:"text,omitempty"`
	InlineData *InlineData `json:"inlineData,omitempty"`
}

type InlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // Base64
}

type GeminiGenConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

type GeminiResponse struct {
	Candidates []GeminiCandidate `json:"candidates"`
}

type GeminiCandidate struct {
	Content      GeminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type GeminiModel struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

type GeminiModelList struct {
	Models []GeminiModel `json:"models"`
}
