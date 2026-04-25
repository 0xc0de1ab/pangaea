package mattermost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPost_Success(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	if err := c.Post(context.Background(), srv.URL+"/hooks/secret", "hi"); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got["text"] != "hi" {
		t.Fatalf("body = %+v", got)
	}
}

func TestPost_HTTPErrorScrubbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad token: SECRET-XYZ"))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	err := c.Post(context.Background(), srv.URL+"/hooks/SECRET-XYZ", "hi")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "SECRET-XYZ") {
		t.Fatalf("error leaks secret: %v", err)
	}
}
