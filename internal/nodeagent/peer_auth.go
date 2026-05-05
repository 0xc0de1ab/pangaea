package nodeagent

import (
	"net/http"
	"strings"
)

func routerPeerDialHeader(token string) http.Header {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	return headers
}
