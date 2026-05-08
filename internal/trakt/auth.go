package trakt

import (
	"context"
	"fmt"
	"time"
)

// AuthBucket is the store bucket name used for OAuth token storage.
const AuthBucket = "trakt_auth"

// StoredToken is the persisted OAuth token record.
type StoredToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// tokenBucket is the minimal store.Bucket interface needed for token operations.
type tokenBucket interface {
	Put(key string, value any) error
	Get(key string, dest any) (bool, error)
}

// SaveToken persists a token in the bucket, keyed by client ID.
func SaveToken(bucket tokenBucket, clientID string, tok *Token) error {
	return bucket.Put(clientID, StoredToken{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    tok.ExpiresAt(),
	})
}

// LoadToken retrieves a stored token by client ID. Returns (nil, false) if none exists.
func LoadToken(bucket tokenBucket, clientID string) (*StoredToken, bool) {
	var st StoredToken
	found, _ := bucket.Get(clientID, &st)
	if !found {
		return nil, false
	}
	return &st, true
}

// GetValidAccessToken returns a current access token for the given client ID,
// refreshing it automatically if it expires within 7 days. Returns an error if
// no token is stored — the user must run `pipeliner auth trakt` first.
func GetValidAccessToken(ctx context.Context, bucket tokenBucket, clientID, clientSecret string) (string, error) {
	st, ok := LoadToken(bucket, clientID)
	if !ok {
		return "", fmt.Errorf("trakt: no stored token for client %q — run: pipeliner auth trakt --client-id=... --client-secret=...", clientID)
	}

	// Refresh proactively if expiring within 7 days.
	if time.Until(st.ExpiresAt) < 7*24*time.Hour {
		tok, err := RefreshToken(ctx, clientID, clientSecret, st.RefreshToken)
		if err != nil {
			// Fall back to existing token if it hasn't actually expired yet.
			if time.Now().Before(st.ExpiresAt) {
				return st.AccessToken, nil
			}
			return "", fmt.Errorf("trakt: token expired and refresh failed: %w", err)
		}
		if err := SaveToken(bucket, clientID, tok); err != nil {
			return "", fmt.Errorf("trakt: save refreshed token: %w", err)
		}
		return tok.AccessToken, nil
	}

	return st.AccessToken, nil
}
