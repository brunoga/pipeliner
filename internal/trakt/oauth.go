package trakt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DeviceCode is returned by RequestDeviceCode and used to drive the device auth flow.
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"` // seconds until code expires
	Interval        int    `json:"interval"`    // minimum polling interval in seconds
}

// Token is the OAuth token pair returned after a successful authorization or refresh.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`  // seconds from CreatedAt
	CreatedAt    int64  `json:"created_at"`  // unix timestamp
	TokenType    string `json:"token_type"`
}

// ExpiresAt returns the absolute expiry time of the token.
func (t *Token) ExpiresAt() time.Time {
	return time.Unix(t.CreatedAt, 0).Add(time.Duration(t.ExpiresIn) * time.Second)
}

// RequestDeviceCode begins the device authorization flow by requesting a
// device code and user code from Trakt. Show DeviceCode.VerificationURL and
// DeviceCode.UserCode to the user, then call ExchangeDeviceCode to poll for
// the token.
func RequestDeviceCode(ctx context.Context, clientID string) (*DeviceCode, error) {
	body, _ := json.Marshal(map[string]string{"client_id": clientID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+"/oauth/device/code", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trakt: request device code: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("trakt: request device code: HTTP %d", resp.StatusCode)
	}

	var dc DeviceCode
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return nil, fmt.Errorf("trakt: decode device code: %w", err)
	}
	return &dc, nil
}

// ExchangeDeviceCode polls Trakt until the user authorizes or the code expires.
// It blocks, returning the token on success or an error on expiry/cancellation.
func ExchangeDeviceCode(ctx context.Context, clientID, clientSecret, deviceCode string, interval, expiresIn int) (*Token, error) {
	if interval <= 0 {
		interval = 5
	}
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("trakt: device code expired")
		}

		tok, pending, err := pollDeviceToken(ctx, clientID, clientSecret, deviceCode)
		if err != nil {
			return nil, err
		}
		if !pending {
			return tok, nil
		}
	}
}

// pollDeviceToken makes a single attempt to exchange the device code for a token.
// Returns (token, false, nil) on success, (nil, true, nil) when still pending,
// or (nil, false, err) on a terminal error.
func pollDeviceToken(ctx context.Context, clientID, clientSecret, code string) (*Token, bool, error) {
	body, _ := json.Marshal(map[string]string{
		"code":          code,
		"client_id":     clientID,
		"client_secret": clientSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+"/oauth/device/token", bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("trakt: poll token: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var tok Token
		if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
			return nil, false, fmt.Errorf("trakt: decode token: %w", err)
		}
		return &tok, false, nil
	case http.StatusBadRequest, http.StatusTooManyRequests:
		// authorization_pending or slow_down — keep polling.
		return nil, true, nil
	case http.StatusGone:
		return nil, false, fmt.Errorf("trakt: device code expired")
	case http.StatusConflict:
		return nil, false, fmt.Errorf("trakt: device code already used")
	default:
		return nil, false, fmt.Errorf("trakt: poll token: HTTP %d", resp.StatusCode)
	}
}

// RefreshToken exchanges a refresh token for a new access + refresh token pair.
func RefreshToken(ctx context.Context, clientID, clientSecret, refreshToken string) (*Token, error) {
	body, _ := json.Marshal(map[string]string{
		"refresh_token": refreshToken,
		"client_id":     clientID,
		"client_secret": clientSecret,
		"redirect_uri":  "urn:ietf:wg:oauth:2.0:oob",
		"grant_type":    "refresh_token",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+"/oauth/token", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trakt: refresh token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("trakt: refresh token: HTTP %d", resp.StatusCode)
	}

	var tok Token
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("trakt: decode refreshed token: %w", err)
	}
	return &tok, nil
}
