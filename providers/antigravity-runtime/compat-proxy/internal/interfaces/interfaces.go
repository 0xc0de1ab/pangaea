package interfaces

import (
	"context"

	"github.com/google/antigravity-compat-proxy/internal/models"
)

// --- Scraper Interfaces ---

type TokenReader interface {
	GetLatestToken() (string, error)
}

type TokenWatcher interface {
	WatchTokenChanges(ctx context.Context) (<-chan string, error)
}

// AuthProvider encapsulates all authentication related operations.
type AuthProvider interface {
	TokenReader
	TokenWatcher
}

// --- Bridge Interfaces ---

type ModelResponse struct {
	Content   string
	ToolCalls []models.ToolCall
	Usage     *models.UsageReport
}

type StreamChunk struct {
	Content   string
	ToolCalls []models.ToolCall
	Done      bool
	Usage     *models.UsageReport
	Error     error
}

type ProviderError struct {
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type ModelInvoker interface {
	Invoke(ctx context.Context, model string, prompt string, tools []models.ToolDefinition, media []models.Media) (*ModelResponse, error)
}

type StreamInvoker interface {
	InvokeStream(ctx context.Context, model string, prompt string, tools []models.ToolDefinition, media []models.Media) (<-chan *StreamChunk, error)
}

// EngineBridge encapsulates the AI engine communication.
type EngineBridge interface {
	ModelInvoker
	StreamInvoker
	GetModels(ctx context.Context) ([]string, error)
	GetDetailedModels(ctx context.Context) (map[string]models.ModelDetail, error)
	GetUsage(ctx context.Context) (map[string]int, error)
	GetAccount(ctx context.Context) (*models.UserStatus, error)
	SetCoreCSRF(token string)
	VerifyProtocol(ctx context.Context) error
	UpdateBackend(ctx context.Context) error
}

// --- Process Management Interfaces ---

type ProcessController interface {
	Start() error
	Stop() error
	Restart() error
}

type HealthChecker interface {
	IsHealthy() bool
}

// LifecycleManager handles the Antigravity server processes.
type LifecycleManager interface {
	ProcessController
	HealthChecker
}
