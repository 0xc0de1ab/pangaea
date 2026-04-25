package discord

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
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	if err := c.Post(context.Background(), srv.URL+"/api/webhooks/123/SECRET", "hi"); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got["content"] != "hi" {
		t.Fatalf("body = %+v", got)
	}
}

func TestPost_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad webhook"))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	if err := c.Post(context.Background(), srv.URL+"/x", "hi"); err == nil {
		t.Fatal("expected error")
	} else if !strings.Contains(err.Error(), "400") {
		t.Fatalf("err = %v", err)
	}
}

func TestPost_EmptyURLRejected(t *testing.T) {
	if err := New().Post(context.Background(), "", "hi"); err == nil {
		t.Fatal("expected error")
	}
}
