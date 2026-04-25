package teams

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostCard_Success(t *testing.T) {
	var got Card
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte("1"))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	err := c.PostCard(context.Background(), srv.URL+"/webhook/secret", Card{
		Title: "T", Text: "body", ThemeColor: "0078D4",
	})
	if err != nil {
		t.Fatalf("PostCard: %v", err)
	}
	if got.Type != "MessageCard" || got.Context != "https://schema.org/extensions" {
		t.Fatalf("envelope auto-fill missing: %+v", got)
	}
	if got.Title != "T" || got.Text != "body" || got.ThemeColor != "0078D4" {
		t.Fatalf("got = %+v", got)
	}
}

func TestPostCard_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal"))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	err := c.PostCard(context.Background(), srv.URL+"/x", Card{Text: "hi"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v", err)
	}
}
