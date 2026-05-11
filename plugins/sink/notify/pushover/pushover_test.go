package pushover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunoga/pipeliner/internal/notify"
)

func TestPushoverSend(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		r.ParseForm()
		if r.FormValue("token") != "test-token" {
			t.Errorf("expected token test-token, got %s", r.FormValue("token"))
		}
		if r.FormValue("user") != "test-user" {
			t.Errorf("expected user test-user, got %s", r.FormValue("user"))
		}
		if r.FormValue("title") != "test-title" {
			t.Errorf("expected title test-title, got %s", r.FormValue("title"))
		}
		if r.FormValue("message") != "test-body" {
			t.Errorf("expected message test-body, got %s", r.FormValue("message"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	n := &pushoverNotifier{
		user:   "test-user",
		token:  "test-token",
		url:    ts.URL,
		client: ts.Client(),
	}

	err := n.Send(context.Background(), notify.Message{
		Title: "test-title",
		Body:  "test-body",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
}
