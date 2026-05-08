package compat

import (
	"strings"
)

func imageURLContentPart(rawURL string) ContentPart {
	url := strings.TrimSpace(rawURL)
	if mime, data, ok := parseDataURL(url); ok {
		return ContentPart{Type: ContentPartImage, MIME: mime, Data: data}
	}
	return ContentPart{Type: ContentPartImage, URL: url}
}

func parseDataURL(value string) (string, string, bool) {
	if !strings.HasPrefix(value, "data:") {
		return "", "", false
	}
	header, data, found := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !found || data == "" {
		return "", "", false
	}
	mime := "application/octet-stream"
	for i, part := range strings.Split(header, ";") {
		if i == 0 && strings.TrimSpace(part) != "" {
			mime = strings.TrimSpace(part)
			break
		}
	}
	return mime, data, true
}

func dataURL(mime string, data string) string {
	mime = strings.TrimSpace(mime)
	if mime == "" {
		mime = "application/octet-stream"
	}
	return "data:" + mime + ";base64," + data
}
