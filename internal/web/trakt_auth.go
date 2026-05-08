package web

import (
	"context"
	"encoding/json"
	"log/slog"
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
		slog.Debug("trakt auth: bad request", "err", err)
		http.Error(w, "client_id and client_secret are required", http.StatusBadRequest)
		return
	}
	slog.Debug("trakt auth: requesting device code", "client_id", req.ClientID)

	// Request device code before touching shared state; use a short timeout
	// so a slow or unreachable Trakt API doesn't hang the browser indefinitely.
	dcCtx, dcCancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer dcCancel()
	dc, err := trakt.RequestDeviceCode(dcCtx, req.ClientID)
	if err != nil {
		slog.Debug("trakt auth: device code request failed", "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	slog.Debug("trakt auth: got device code", "user_code", dc.UserCode, "expires_in", dc.ExpiresIn)

	// Cancel any in-flight session, then install the new one.
	s.traktAuthMu.Lock()
	if s.traktAuth != nil && s.traktAuth.cancel != nil {
		s.traktAuth.cancel()
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
	slog.Debug("trakt auth: session installed, starting polling goroutine")

	go func() {
		defer cancel()
		slog.Debug("trakt auth: polling for token")
		tok, err := trakt.ExchangeDeviceCode(ctx, req.ClientID, req.ClientSecret, dc.DeviceCode, dc.Interval, dc.ExpiresIn)
		sess.mu.Lock()
		defer sess.mu.Unlock()
		if err != nil {
			slog.Debug("trakt auth: exchange failed", "err", err, "ctx_err", ctx.Err())
			if ctx.Err() != nil {
				sess.status = "error"
				sess.message = "authorization cancelled or timed out"
			} else {
				sess.status = "error"
				sess.message = err.Error()
			}
			return
		}
		slog.Debug("trakt auth: token received, saving")
		bucket := s.db.Bucket(trakt.AuthBucket)
		if saveErr := trakt.SaveToken(bucket, req.ClientID, tok); saveErr != nil {
			slog.Debug("trakt auth: save failed", "err", saveErr)
			sess.status = "error"
			sess.message = "token received but could not be saved: " + saveErr.Error()
			return
		}
		slog.Debug("trakt auth: authorized and saved")
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
