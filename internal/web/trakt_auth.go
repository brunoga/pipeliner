package web

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/brunoga/pipeliner/internal/trakt"
)

type traktAuthSession struct {
	mu           sync.Mutex
	clientID     string
	clientSecret string
	userCode     string
	verifyURL    string
	expiresAt    time.Time
	status       string // "pending", "authorized", "error"
	message      string
	cancel       context.CancelFunc
}

func (s *Server) apiTraktAuthStart(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ClientID == "" || req.ClientSecret == "" {
		http.Error(w, "client_id and client_secret are required", http.StatusBadRequest)
		return
	}

	// Cancel any in-flight session.
	s.traktAuthMu.Lock()
	if s.traktAuth != nil && s.traktAuth.cancel != nil {
		s.traktAuth.cancel()
	}

	dc, err := trakt.RequestDeviceCode(r.Context(), req.ClientID)
	if err != nil {
		s.traktAuthMu.Unlock()
		http.Error(w, "trakt: "+err.Error(), http.StatusBadGateway)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(dc.ExpiresIn)*time.Second)
	sess := &traktAuthSession{
		clientID:     req.ClientID,
		clientSecret: req.ClientSecret,
		userCode:     dc.UserCode,
		verifyURL:    dc.VerificationURL,
		expiresAt:    time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second),
		status:       "pending",
		cancel:       cancel,
	}
	s.traktAuth = sess
	s.traktAuthMu.Unlock()

	go func() {
		defer cancel()
		tok, err := trakt.ExchangeDeviceCode(ctx, req.ClientID, req.ClientSecret, dc.DeviceCode, dc.Interval, dc.ExpiresIn)
		sess.mu.Lock()
		defer sess.mu.Unlock()
		if err != nil {
			if ctx.Err() != nil {
				sess.status = "error"
				sess.message = "authorization cancelled or timed out"
			} else {
				sess.status = "error"
				sess.message = err.Error()
			}
			return
		}
		bucket := s.db.Bucket(trakt.AuthBucket)
		if saveErr := trakt.SaveToken(bucket, req.ClientID, tok); saveErr != nil {
			sess.status = "error"
			sess.message = "token received but could not be saved: " + saveErr.Error()
			return
		}
		sess.status = "authorized"
	}()

	writeJSON(w, map[string]any{
		"user_code":        dc.UserCode,
		"verification_url": dc.VerificationURL,
		"expires_in":       dc.ExpiresIn,
	})
}

func (s *Server) apiTraktAuthPoll(w http.ResponseWriter, _ *http.Request) {
	s.traktAuthMu.Lock()
	sess := s.traktAuth
	s.traktAuthMu.Unlock()

	if sess == nil {
		writeJSON(w, map[string]string{"status": "idle"})
		return
	}

	sess.mu.Lock()
	status := sess.status
	message := sess.message
	sess.mu.Unlock()

	writeJSON(w, map[string]string{"status": status, "message": message})
}
