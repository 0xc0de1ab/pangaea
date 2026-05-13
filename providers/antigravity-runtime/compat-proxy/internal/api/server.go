package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/antigravity-compat-proxy/internal/interfaces"
	"github.com/google/antigravity-compat-proxy/internal/models"
)

type Server struct {
	engine  interfaces.EngineBridge
	router  *gin.Engine
	keys    APIKeys
	version string
}

func NewServer(engine interfaces.EngineBridge, keys APIKeys, version string) *Server {
	s := &Server{
		engine:  engine,
		router:  gin.Default(),
		keys:    keys,
		version: version,
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	v1 := s.router.Group("/v1")
	v1.Use(AuthMiddleware(s.keys))

	v1.GET("/health", s.handleHealth)
	v1.GET("/usage", s.handleUsage)
	v1.GET("/models", s.handleOpenAIModels)
	v1.GET("/models/status", s.handleDetailedModels)
	v1.GET("/account", s.handleAccount)
	v1.POST("/chat/completions", s.handleChatCompletions)
	v1.POST("/messages", s.handleAnthropicMessages)

	v1beta := s.router.Group("/v1beta")
	v1beta.Use(AuthMiddleware(s.keys))
	v1beta.GET("/models", s.handleGeminiModels)
	v1beta.POST("/models/:model", s.handleGeminiGenerateContent)
}

func (s *Server) handleChatCompletions(c *gin.Context) {
	var req models.ChatCompletionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	prompt, media := TranscodeMessages(req.Messages)
	tools := TranscodeOpenAITools(req.Tools)

	if req.Stream {
		s.handleStreamingChat(c, req.Model, prompt, tools, media)
	} else {
		s.handleUnaryChat(c, req.Model, prompt, tools, media)
	}
}

func (s *Server) handleAnthropicMessages(c *gin.Context) {
	var req models.AnthropicRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	prompt, media := TranscodeAnthropicMessages(req)
	tools := TranscodeAnthropicTools(req.Tools)

	if req.Stream {
		s.handleStreamingAnthropic(c, req.Model, prompt, tools, media)
	} else {
		s.handleUnaryAnthropic(c, req.Model, prompt, tools, media)
	}
}

func (s *Server) handleGeminiGenerateContent(c *gin.Context) {
	var req models.GeminiRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	model := c.Param("model")
	stream := strings.HasSuffix(model, ":streamGenerateContent")
	model = strings.TrimSuffix(model, ":streamGenerateContent")
	model = strings.TrimSuffix(model, ":generateContent")
	prompt, media := TranscodeGeminiMessages(req)
	if stream {
		s.handleStreamingGemini(c, model, prompt, media)
		return
	}

	resp, err := s.engine.Invoke(c.Request.Context(), model, prompt, nil, media)
	if err != nil {
		s.writeProviderError(c, err, "gemini")
		return
	}

	c.JSON(http.StatusOK, models.GeminiResponse{
		Candidates: []models.GeminiCandidate{
			{
				Content: models.GeminiContent{
					Role:  "model",
					Parts: []models.GeminiPart{{Text: resp.Content}},
				},
				FinishReason: "STOP",
			},
		},
	})
}

func (s *Server) handleUnaryChat(c *gin.Context, model string, prompt string, tools []models.ToolDefinition, media []models.Media) {
	resp, err := s.engine.Invoke(c.Request.Context(), model, prompt, tools, media)
	if err != nil {
		s.writeProviderError(c, err, "openai")
		return
	}

	toolCalls := ParseToolCalls(resp.Content)

	oaResp := models.ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().Unix()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []models.ChatCompletionChoice{
			{
				Index: 0,
				Message: models.ChatMessage{
					Role:      "assistant",
					Content:   resp.Content,
					ToolCalls: toolCalls,
				},
				FinishReason: "stop",
			},
		},
		Usage: resp.Usage,
	}

	if len(toolCalls) > 0 {
		oaResp.Choices[0].FinishReason = "tool_calls"
	}

	c.JSON(http.StatusOK, oaResp)
}

func (s *Server) handleStreamingChat(c *gin.Context, model string, prompt string, tools []models.ToolDefinition, media []models.Media) {
	chunks, err := s.engine.InvokeStream(c.Request.Context(), model, prompt, tools, media)
	if err != nil {
		s.writeProviderError(c, err, "openai")
		return
	}

	first, ok := <-chunks
	if !ok {
		s.writeProviderError(c, &interfaces.ProviderError{StatusCode: http.StatusBadGateway, Code: "empty_stream", Message: "upstream stream ended without a response"}, "openai")
		return
	}
	if first.Error != nil {
		s.writeProviderError(c, first.Error, "openai")
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	id := fmt.Sprintf("chatcmpl-%d", time.Now().Unix())
	created := time.Now().Unix()
	var lastUsage *models.UsageReport
	writeChunk := func(content string, finishReason *string, usage *models.UsageReport) {
		streamResp := models.ChatCompletionStreamResponse{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []models.ChatCompletionStreamChoice{
				{
					Index: 0,
					Delta: models.ChatMessage{
						Content: content,
					},
					FinishReason: finishReason,
				},
			},
			Usage: usage,
		}
		bytes, _ := json.Marshal(streamResp)
		fmt.Fprintf(c.Writer, "data: %s\n\n", string(bytes))
		c.Writer.Flush()
	}

	handleChunk := func(chunk *interfaces.StreamChunk) bool {
		if chunk == nil {
			return true
		}
		if chunk.Error != nil {
			s.writeOpenAIStreamError(c, chunk.Error)
			return false
		}
		if chunk.Usage != nil {
			lastUsage = chunk.Usage
		}
		if chunk.Content == "" {
			return true
		}
		writeChunk(chunk.Content, nil, nil)
		return true
	}

	if !handleChunk(first) {
		return
	}
	for chunk := range chunks {
		if !handleChunk(chunk) {
			return
		}
	}
	stop := "stop"
	writeChunk("", &stop, lastUsage)
	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()
}

func (s *Server) handleUnaryAnthropic(c *gin.Context, model string, prompt string, tools []models.ToolDefinition, media []models.Media) {
	resp, err := s.engine.Invoke(c.Request.Context(), model, prompt, tools, media)
	if err != nil {
		s.writeProviderError(c, err, "anthropic")
		return
	}

	toolCalls := ParseToolCalls(resp.Content)
	var content []interface{}

	if len(toolCalls) > 0 {
		for _, tc := range toolCalls {
			var args map[string]interface{}
			json.Unmarshal([]byte(tc.Function.Arguments), &args)
			content = append(content, models.AnthropicContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: args,
			})
		}
	} else {
		content = append(content, models.AnthropicContentBlock{
			Type: "text",
			Text: resp.Content,
		})
	}

	antResp := models.AnthropicResponse{
		ID:      fmt.Sprintf("msg_%d", time.Now().Unix()),
		Type:    "message",
		Role:    "assistant",
		Model:   model,
		Content: content,
	}

	c.JSON(http.StatusOK, antResp)
}

func (s *Server) handleStreamingAnthropic(c *gin.Context, model string, prompt string, tools []models.ToolDefinition, media []models.Media) {
	chunks, err := s.engine.InvokeStream(c.Request.Context(), model, prompt, tools, media)
	if err != nil {
		s.writeProviderError(c, err, "anthropic")
		return
	}

	first, ok := <-chunks
	if !ok {
		s.writeProviderError(c, &interfaces.ProviderError{StatusCode: http.StatusBadGateway, Code: "empty_stream", Message: "upstream stream ended without a response"}, "anthropic")
		return
	}
	if first.Error != nil {
		s.writeProviderError(c, first.Error, "anthropic")
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	fmt.Fprintf(c.Writer, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"role\":\"assistant\",\"model\":%q}}\n\n", model)
	if !s.writeAnthropicStreamChunk(c, first) {
		return
	}
	for chunk := range chunks {
		if chunk.Error != nil {
			s.writeAnthropicStreamError(c, chunk.Error)
			return
		}
		if !s.writeAnthropicStreamChunk(c, chunk) {
			return
		}
	}
	fmt.Fprintf(c.Writer, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	c.Writer.Flush()
}

func (s *Server) handleStreamingGemini(c *gin.Context, model string, prompt string, media []models.Media) {
	chunks, err := s.engine.InvokeStream(c.Request.Context(), model, prompt, nil, media)
	if err != nil {
		s.writeProviderError(c, err, "gemini")
		return
	}

	first, ok := <-chunks
	if !ok {
		s.writeProviderError(c, &interfaces.ProviderError{StatusCode: http.StatusBadGateway, Code: "empty_stream", Message: "upstream stream ended without a response"}, "gemini")
		return
	}
	if first.Error != nil {
		s.writeProviderError(c, first.Error, "gemini")
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	if !s.writeGeminiStreamChunk(c, first) {
		return
	}
	for chunk := range chunks {
		if chunk.Error != nil {
			s.writeGeminiStreamError(c, chunk.Error)
			return
		}
		if !s.writeGeminiStreamChunk(c, chunk) {
			return
		}
	}
	payload := models.GeminiResponse{
		Candidates: []models.GeminiCandidate{
			{
				Content: models.GeminiContent{
					Role: "model",
				},
				FinishReason: "STOP",
			},
		},
	}
	bytes, _ := json.Marshal(payload)
	fmt.Fprintf(c.Writer, "data: %s\n\n", string(bytes))
	c.Writer.Flush()
}

func (s *Server) writeAnthropicStreamChunk(c *gin.Context, chunk *interfaces.StreamChunk) bool {
	if chunk == nil || chunk.Content == "" {
		return true
	}
	payload, _ := json.Marshal(gin.H{
		"type":  "content_block_delta",
		"index": 0,
		"delta": gin.H{
			"type": "text_delta",
			"text": chunk.Content,
		},
	})
	fmt.Fprintf(c.Writer, "event: content_block_delta\ndata: %s\n\n", string(payload))
	c.Writer.Flush()
	return true
}

func (s *Server) writeGeminiStreamChunk(c *gin.Context, chunk *interfaces.StreamChunk) bool {
	if chunk == nil || chunk.Content == "" {
		return true
	}
	payload := models.GeminiResponse{
		Candidates: []models.GeminiCandidate{
			{
				Content: models.GeminiContent{
					Role:  "model",
					Parts: []models.GeminiPart{{Text: chunk.Content}},
				},
			},
		},
	}
	bytes, _ := json.Marshal(payload)
	fmt.Fprintf(c.Writer, "data: %s\n\n", string(bytes))
	c.Writer.Flush()
	return true
}

func (s *Server) writeProviderError(c *gin.Context, err error, protocol string) {
	providerErr := asProviderError(err)
	status := providerErr.StatusCode
	if status == 0 {
		status = http.StatusInternalServerError
	}
	switch protocol {
	case "anthropic":
		resp := models.AnthropicErrorResponse{Type: "error"}
		resp.Error.Type = providerErr.Code
		resp.Error.Message = providerErr.Message
		c.JSON(status, resp)
	case "gemini":
		resp := models.GeminiErrorResponse{}
		resp.Error.Code = status
		resp.Error.Message = providerErr.Message
		resp.Error.Status = providerErr.Code
		c.JSON(status, resp)
	default:
		resp := models.OpenAIErrorResponse{}
		resp.Error.Message = providerErr.Message
		resp.Error.Type = providerErr.Code
		resp.Error.Code = providerErr.Code
		c.JSON(status, resp)
	}
}

func (s *Server) writeOpenAIStreamError(c *gin.Context, err error) {
	providerErr := asProviderError(err)
	payload := models.OpenAIErrorResponse{}
	payload.Error.Message = providerErr.Message
	payload.Error.Type = providerErr.Code
	payload.Error.Code = providerErr.Code
	data, _ := json.Marshal(payload)
	fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", string(data))
	c.Writer.Flush()
}

func (s *Server) writeAnthropicStreamError(c *gin.Context, err error) {
	providerErr := asProviderError(err)
	payload := models.AnthropicErrorResponse{Type: "error"}
	payload.Error.Type = providerErr.Code
	payload.Error.Message = providerErr.Message
	data, _ := json.Marshal(payload)
	fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", string(data))
	c.Writer.Flush()
}

func (s *Server) writeGeminiStreamError(c *gin.Context, err error) {
	providerErr := asProviderError(err)
	payload := models.GeminiErrorResponse{}
	payload.Error.Code = providerErr.StatusCode
	payload.Error.Message = providerErr.Message
	payload.Error.Status = providerErr.Code
	data, _ := json.Marshal(payload)
	fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", string(data))
	c.Writer.Flush()
}

func asProviderError(err error) *interfaces.ProviderError {
	if err == nil {
		return &interfaces.ProviderError{
			StatusCode: http.StatusInternalServerError,
			Code:       "upstream_error",
			Message:    "upstream error",
		}
	}
	var providerErr *interfaces.ProviderError
	if errors.As(err, &providerErr) {
		if providerErr.StatusCode == 0 {
			providerErr.StatusCode = http.StatusInternalServerError
		}
		if providerErr.Code == "" {
			providerErr.Code = "upstream_error"
		}
		if providerErr.Message == "" {
			providerErr.Message = err.Error()
		}
		return providerErr
	}
	return &interfaces.ProviderError{
		StatusCode: http.StatusInternalServerError,
		Code:       "upstream_error",
		Message:    err.Error(),
	}
}

func (s *Server) handleOpenAIModels(c *gin.Context) {
	ids, err := s.engine.GetModels(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var data []models.OpenAIModel
	for _, id := range ids {
		data = append(data, models.OpenAIModel{
			ID:      id,
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "antigravity",
		})
	}

	c.JSON(http.StatusOK, models.OpenAIModelList{
		Object: "list",
		Data:   data,
	})
}

func (s *Server) handleGeminiModels(c *gin.Context) {
	ids, err := s.engine.GetModels(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var data []models.GeminiModel
	for _, id := range ids {
		data = append(data, models.GeminiModel{
			Name:        "models/" + id,
			DisplayName: id,
			Description: "Antigravity Model",
		})
	}

	c.JSON(http.StatusOK, models.GeminiModelList{
		Models: data,
	})
}

func (s *Server) handleDetailedModels(c *gin.Context) {
	details, err := s.engine.GetDetailedModels(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, details)
}

func (s *Server) handleAccount(c *gin.Context) {
	acc, err := s.engine.GetAccount(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, acc)
}

func (s *Server) handleUsage(c *gin.Context) {
	usage, err := s.engine.GetUsage(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total_tokens":           usage["total_tokens"],
		"remaining_tokens":       usage["remaining_tokens"],
		"reset_time":             usage["reset_time"],
		"remaining_fraction_pct": usage["remaining_fraction_pct"],
	})
}

func (s *Server) handleHealth(c *gin.Context) {
	serverVersion, serverCommit := detectAntigravityServerVersion()
	status := http.StatusOK
	body := gin.H{
		"status":         "healthy",
		"proxy":          "up",
		"version":        s.version,
		"proxy_version":  s.version,
		"server_version": serverVersion,
		"server_commit":  serverCommit,
		"target_version": serverVersion,
		"time":           time.Now().Format(time.RFC3339),
	}
	if _, err := s.engine.GetDetailedModels(c.Request.Context()); err != nil {
		status = http.StatusServiceUnavailable
		body["status"] = "degraded"
		body["core"] = "unavailable"
		body["error"] = err.Error()
	}
	c.JSON(status, body)
}

func detectAntigravityServerVersion() (string, string) {
	paths := []string{
		strings.TrimSpace(os.Getenv("ANTIGRAVITY_PRODUCT_PATH")),
		"/opt/antigravity-server/product.json",
		"/opt/antigravity-server/package.json",
		"/opt/antigravity/server/product.json",
		"/opt/antigravity/server/package.json",
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var parsed struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			continue
		}
		if strings.TrimSpace(parsed.Version) != "" {
			return strings.TrimSpace(parsed.Version), strings.TrimSpace(parsed.Commit)
		}
	}
	return "", ""
}

func (s *Server) Handler() http.Handler {
	return s.router
}

func (s *Server) Run(addr string) error {
	fmt.Printf("Proxy server listening on %s\n", addr)
	return s.router.Run(addr)
}
