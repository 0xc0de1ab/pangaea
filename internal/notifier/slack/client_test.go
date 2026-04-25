package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostMessage_Success(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	err := c.PostMessage(context.Background(), srv.URL+"/services/T/B/SECRET", "hello world")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if gotBody["text"] != "hello world" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestPostMessage_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid_payload"))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	err := c.PostMessage(context.Background(), srv.URL+"/services/T/B/SECRET", "x")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("err = %v", err)
	}
}

func TestPostMessage_SecretScrubbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad token: SUPER-SECRET"))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	err := c.PostMessage(context.Background(), srv.URL+"/services/T/B/SUPER-SECRET", "x")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "SUPER-SECRET") {
		t.Fatalf("error leaks secret: %v", err)
	}
}

func TestPostMessage_EmptyURLRejected(t *testing.T) {
	c := New()
	err := c.PostMessage(context.Background(), "", "x")
	if err == nil {
		t.Fatal("expected error")
	}
}
