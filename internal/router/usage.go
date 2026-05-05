package router

import (
	"strings"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/quota"
)

func EstimateQuotaUsage(request compat.Request) quota.Usage {
	chars := len(request.Model)
	for _, message := range request.Messages {
		chars += len(message.Role)
		for _, part := range message.Content {
			chars += len(part.Text)
		}
		for _, call := range message.ToolCalls {
			chars += len(call.Name) + len(call.Arguments)
		}
	}
	tokens := int64(chars / 4)
	if chars > 0 && tokens == 0 {
		tokens = 1
	}
	return quota.Usage{Tokens: tokens, Requests: 1}
}

func OpenAIQuotaScope(requestID string, route RouteRequest, request compat.Request) quota.Scope {
	return CanonicalQuotaScope(requestID, route, request)
}

func CanonicalQuotaScope(requestID string, route RouteRequest, request compat.Request) quota.Scope {
	scope := quota.Scope{
		TenantID: route.TenantID,
		UserID:   route.UserID,
		APIKeyID: route.APIKeyID,
		Model:    route.Model,
	}
	if strings.TrimSpace(scope.APIKeyID) == "" {
		scope.APIKeyID = requestID
	}
	if strings.TrimSpace(scope.Model) == "" {
		scope.Model = request.Model
	}
	return scope
}
