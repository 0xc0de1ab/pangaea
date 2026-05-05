package provider

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var ErrProviderUpstream = errors.New("provider upstream error")

type UpstreamError struct {
	StatusCode int
	Code       string
	Message    string
	Body       string
	RetryAfter string
}

func (e *UpstreamError) Error() string {
	if e == nil {
		return ErrProviderUpstream.Error()
	}
	message := strings.TrimSpace(e.Message)
	if message == "" && e.StatusCode > 0 {
		message = http.StatusText(e.StatusCode)
	}
	if message == "" {
		message = "upstream request failed"
	}
	parts := []string{ErrProviderUpstream.Error()}
	if e.StatusCode > 0 {
		parts = append(parts, fmt.Sprintf("status=%d", e.StatusCode))
	}
	if code := strings.TrimSpace(e.Code); code != "" {
		parts = append(parts, "code="+code)
	}
	parts = append(parts, "message="+message)
	return strings.Join(parts, ": ")
}

func (e *UpstreamError) Unwrap() error {
	return ErrProviderUpstream
}

func (e *UpstreamError) RouterStatusCode() int {
	if e == nil {
		return http.StatusBadGateway
	}
	switch e.StatusCode {
	case http.StatusTooManyRequests:
		return http.StatusTooManyRequests
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return http.StatusGatewayTimeout
	case http.StatusServiceUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}
