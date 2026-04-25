package ntfy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPost_Success(t *testing.T) {
	var gotBody string
	var gotTitle, gotPriority, gotTags, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		gotTags = r.Header.Get("Tags")
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), AuthToken: "tk"}
	err := c.Post(context.Background(), srv.URL+"/my-topic", "hello", PostOptions{
		Title: "T", Priority: 4, Tags: "warning,fire",
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if gotBody != "hello" || gotTitle != "T" || gotPriority != "4" || gotTags != "warning,fire" {
		t.Fatalf("got body=%q title=%q prio=%q tags=%q", gotBody, gotTitle, gotPriority, gotTags)
	}
	if gotAuth != "Bearer tk" {
		t.Fatalf("auth = %q", gotAuth)
	}
}

func TestPost_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden"))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	err := c.Post(context.Background(), srv.URL+"/x", "hi", PostOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
}

func TestPost_PriorityClampedToHeader(t *testing.T) {
	var gotPriority string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPriority = r.Header.Get("Priority")
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	// Out-of-range priority should be omitted, not rejected.
	_ = c.Post(context.Background(), srv.URL+"/x", "hi", PostOptions{Priority: 99})
	if gotPriority != "" {
		t.Fatalf("Priority header should be empty for out-of-range, got %q", gotPriority)
	}
}
