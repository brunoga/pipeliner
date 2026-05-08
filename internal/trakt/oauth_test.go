package trakt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func oauthServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, h := range handlers {
		mux.HandleFunc(path, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func TestRequestDeviceCode(t *testing.T) {
	srv := oauthServer(t, map[string]http.HandlerFunc{
		"/oauth/device/code": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "bad method", http.StatusMethodNotAllowed)
				return
			}
			writeJSON(w, map[string]any{
				"device_code":      "dev123",
				"user_code":        "AB12CD",
				"verification_url": "https://trakt.tv/activate",
				"expires_in":       600,
				"interval":         5,
			})
		},
	})
	orig := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = orig })

	dc, err := RequestDeviceCode(context.Background(), "client-id")
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	if dc.DeviceCode != "dev123" {
		t.Errorf("device_code: got %q", dc.DeviceCode)
	}
	if dc.UserCode != "AB12CD" {
		t.Errorf("user_code: got %q", dc.UserCode)
	}
	if dc.ExpiresIn != 600 {
		t.Errorf("expires_in: got %d", dc.ExpiresIn)
	}
	if dc.Interval != 5 {
		t.Errorf("interval: got %d", dc.Interval)
	}
}

func TestRequestDeviceCodeHTTPError(t *testing.T) {
	srv := oauthServer(t, map[string]http.HandlerFunc{
		"/oauth/device/code": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad request", http.StatusBadRequest)
		},
	})
	orig := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = orig })

	_, err := RequestDeviceCode(context.Background(), "client-id")
	if err == nil {
		t.Fatal("expected error on HTTP 400")
	}
}

func TestExchangeDeviceCodeSuccess(t *testing.T) {
	calls := 0
	createdAt := time.Now().Unix()
	srv := oauthServer(t, map[string]http.HandlerFunc{
		"/oauth/device/token": func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls < 2 {
				// First call: pending
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]string{"error": "authorization_pending"})
				return
			}
			// Second call: success
			writeJSON(w, map[string]any{
				"access_token":  "acc123",
				"refresh_token": "ref456",
				"expires_in":    7776000,
				"created_at":    createdAt,
				"token_type":    "Bearer",
			})
		},
	})
	orig := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = orig })

	tok, err := ExchangeDeviceCode(context.Background(), "cid", "csec", "dev123", 0, 30)
	if err != nil {
		t.Fatalf("ExchangeDeviceCode: %v", err)
	}
	if tok.AccessToken != "acc123" {
		t.Errorf("access_token: got %q", tok.AccessToken)
	}
	if tok.RefreshToken != "ref456" {
		t.Errorf("refresh_token: got %q", tok.RefreshToken)
	}
	if calls != 2 {
		t.Errorf("expected 2 poll attempts, got %d", calls)
	}
}

func TestExchangeDeviceCodeExpired(t *testing.T) {
	srv := oauthServer(t, map[string]http.HandlerFunc{
		"/oauth/device/token": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusGone)
		},
	})
	orig := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = orig })

	_, err := ExchangeDeviceCode(context.Background(), "cid", "csec", "dev123", 0, 30)
	if err == nil {
		t.Fatal("expected error on gone device code")
	}
}

func TestExchangeDeviceCodeContextCancelled(t *testing.T) {
	srv := oauthServer(t, map[string]http.HandlerFunc{
		"/oauth/device/token": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]string{"error": "authorization_pending"})
		},
	})
	orig := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = orig })

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := ExchangeDeviceCode(ctx, "cid", "csec", "dev123", 0, 600)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestRefreshToken(t *testing.T) {
	createdAt := time.Now().Unix()
	srv := oauthServer(t, map[string]http.HandlerFunc{
		"/oauth/token": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "bad method", http.StatusMethodNotAllowed)
				return
			}
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
			if body["grant_type"] != "refresh_token" {
				http.Error(w, "bad grant_type", http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]any{
				"access_token":  "newacc",
				"refresh_token": "newref",
				"expires_in":    7776000,
				"created_at":    createdAt,
				"token_type":    "Bearer",
			})
		},
	})
	orig := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = orig })

	tok, err := RefreshToken(context.Background(), "cid", "csec", "oldref")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if tok.AccessToken != "newacc" {
		t.Errorf("access_token: got %q", tok.AccessToken)
	}
	if tok.RefreshToken != "newref" {
		t.Errorf("refresh_token: got %q", tok.RefreshToken)
	}
}

func TestRefreshTokenHTTPError(t *testing.T) {
	srv := oauthServer(t, map[string]http.HandlerFunc{
		"/oauth/token": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		},
	})
	orig := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = orig })

	_, err := RefreshToken(context.Background(), "cid", "csec", "badref")
	if err == nil {
		t.Fatal("expected error on HTTP 401")
	}
}

func TestTokenExpiresAt(t *testing.T) {
	createdAt := time.Now().Truncate(time.Second)
	tok := &Token{
		CreatedAt: createdAt.Unix(),
		ExpiresIn: 7776000, // 90 days
	}
	want := createdAt.Add(7776000 * time.Second)
	got := tok.ExpiresAt()
	if !got.Equal(want) {
		t.Errorf("ExpiresAt: got %v, want %v", got, want)
	}
}
