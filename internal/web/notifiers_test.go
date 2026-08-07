package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	// Register the notifier backends so the endpoint has something to expose.
	_ "github.com/brunoga/pipeliner/plugins/sink/notify/email"
	_ "github.com/brunoga/pipeliner/plugins/sink/notify/pushover"
	_ "github.com/brunoga/pipeliner/plugins/sink/notify/webhook"
)

func TestAPINotifiers(t *testing.T) {
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/notifiers", srv.apiNotifiers)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/notifiers", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out []struct {
		Name   string `json:"name"`
		Schema []struct {
			Key      string `json:"key"`
			Type     string `json:"type"`
			Required bool   `json:"required"`
		} `json:"schema"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	byName := map[string]map[string]string{} // notifier -> key -> type
	for _, n := range out {
		keys := map[string]string{}
		for _, f := range n.Schema {
			keys[f.Key] = f.Type
		}
		byName[n.Name] = keys
	}

	// The whole point: email exposes username and password so the UI can show them.
	email := byName["email"]
	if email == nil {
		t.Fatalf("email notifier missing from response: %v", byName)
	}
	for _, k := range []string{"smtp_host", "smtp_port", "sender", "to", "username", "password", "html"} {
		if _, ok := email[k]; !ok {
			t.Errorf("email schema missing key %q", k)
		}
	}
	if email["smtp_port"] != "int" || email["to"] != "list" || email["html"] != "bool" {
		t.Errorf("email field types wrong: %v", email)
	}
	if byName["pushover"]["token"] != "string" || byName["webhook"]["url"] != "string" {
		t.Errorf("pushover/webhook missing expected keys: %v", byName)
	}
}
