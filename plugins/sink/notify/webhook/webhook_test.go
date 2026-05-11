package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/notify"
)

func TestWebhookSend(t *testing.T) {
	var received payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received) //nolint:errcheck
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type: got %q", ct)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Errorf("Authorization: got %q", auth)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n, err := newNotifier(map[string]any{
		"url":     srv.URL,
		"headers": map[string]any{"Authorization": "Bearer test-token"},
	})
	if err != nil {
		t.Fatal(err)
	}

	e := entry.New("Test Movie", "http://example.com/movie")
	msg := notify.Message{
		Title:   "New download",
		Body:    "1 entry accepted",
		Entries: []*entry.Entry{e},
	}

	if err := n.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if received.Title != "New download" {
		t.Errorf("title: got %q", received.Title)
	}
	if len(received.Entries) != 1 || received.Entries[0].Title != "Test Movie" {
		t.Errorf("entries: got %+v", received.Entries)
	}
}

func TestWebhookServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	n, _ := newNotifier(map[string]any{"url": srv.URL})
	err := n.Send(context.Background(), notify.Message{Title: "test"})
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestMissingURL(t *testing.T) {
	_, err := newNotifier(map[string]any{})
	if err == nil {
		t.Fatal("expected error when url is missing")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := notify.Lookup("webhook")
	if !ok {
		t.Fatal("webhook notifier not registered")
	}
	_, err := d.Factory(map[string]any{"url": "http://example.com"})
	if err != nil {
		t.Fatal(err)
	}
}
