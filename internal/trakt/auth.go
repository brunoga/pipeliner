package trakt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// refreshWindow is how long before expiry a token is refreshed proactively.
const refreshWindow = 7 * 24 * time.Hour

// Refreshes are serialized per client ID. Trakt rotates the refresh token on
// every successful refresh and invalidates the previous one, so two nodes
// refreshing the same token concurrently (a source, a sink, the calendar, and
// the metainfo plugin all call GetValidAccessToken independently) can each
// consume the other's token and permanently break auth. The per-client lock,
// with a re-check after acquiring it, ensures exactly one refresh happens per
// rotation.
var (
	refreshMu    sync.Mutex
	refreshLocks = map[string]*sync.Mutex{}
)

func refreshLock(clientID string) *sync.Mutex {
	refreshMu.Lock()
	defer refreshMu.Unlock()
	m := refreshLocks[clientID]
	if m == nil {
		m = &sync.Mutex{}
		refreshLocks[clientID] = m
	}
	return m
}

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

// listableBucket adds key enumeration, used to report the status of every
// stored token without the caller needing to know the client IDs.
type listableBucket interface {
	tokenBucket
	Keys() ([]string, error)
}

// Statuses returns the status of every stored token (one per client ID),
// skipping the internal auth-error marker keys. Purely informational.
func Statuses(bucket listableBucket) ([]TokenStatus, error) {
	keys, err := bucket.Keys()
	if err != nil {
		return nil, err
	}
	var out []TokenStatus
	for _, k := range keys {
		if strings.ContainsRune(k, '\x00') {
			continue // internal marker key, not a client ID
		}
		if ts, ok := Status(bucket, k); ok {
			out = append(out, ts)
		}
	}
	return out, nil
}

// SaveToken persists a token in the bucket, keyed by client ID. Any recorded
// auth-failure marker is cleared, so a successful (re-)authorization or refresh
// automatically resolves the "needs re-authorization" state in the UI.
func SaveToken(bucket tokenBucket, clientID string, tok *Token) error {
	if err := bucket.Put(clientID, StoredToken{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    tok.ExpiresAt(),
	}); err != nil {
		return err
	}
	clearAuthError(bucket, clientID)
	return nil
}

// storedAuthError is the persisted "last auth failure" marker. An empty Message
// means no outstanding failure.
type storedAuthError struct {
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

func errorKey(clientID string) string { return clientID + "\x00err" }

func recordAuthError(bucket tokenBucket, clientID, msg string) {
	_ = bucket.Put(errorKey(clientID), storedAuthError{Message: msg, At: time.Now()})
}

func clearAuthError(bucket tokenBucket, clientID string) {
	_ = bucket.Put(errorKey(clientID), storedAuthError{})
}

func loadAuthError(bucket tokenBucket, clientID string) (storedAuthError, bool) {
	var e storedAuthError
	found, _ := bucket.Get(errorKey(clientID), &e)
	return e, found && e.Message != ""
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
// refreshing it automatically if it expires within refreshWindow. Returns an
// error if no token is stored — the user must run `pipeliner auth trakt` first.
//
// Refreshes are serialized per client ID with a re-check after locking, so
// concurrent callers don't race on Trakt's single-use refresh-token rotation.
func GetValidAccessToken(ctx context.Context, bucket tokenBucket, clientID, clientSecret string) (string, error) {
	st, ok := LoadToken(bucket, clientID)
	if !ok {
		return "", fmt.Errorf("trakt: no stored token for client %q — run: pipeliner auth trakt --client-id=... --client-secret=...", clientID)
	}
	// Fast path: comfortably valid, no lock needed.
	if time.Until(st.ExpiresAt) >= refreshWindow {
		return st.AccessToken, nil
	}

	// Refresh needed — serialize so only one goroutine rotates the token.
	mu := refreshLock(clientID)
	mu.Lock()
	defer mu.Unlock()

	// Re-check under the lock: another caller may have refreshed while we waited.
	st, ok = LoadToken(bucket, clientID)
	if !ok {
		return "", fmt.Errorf("trakt: no stored token for client %q", clientID)
	}
	if time.Until(st.ExpiresAt) >= refreshWindow {
		return st.AccessToken, nil
	}

	tok, err := RefreshToken(ctx, clientID, clientSecret, st.RefreshToken)
	if err != nil {
		// A rejected refresh token is a dead end — record it so the UI can flag
		// "needs re-authorization", and surface it clearly rather than masking
		// it behind a still-valid access token.
		if errors.Is(err, ErrRefreshRejected) {
			recordAuthError(bucket, clientID, err.Error())
			return "", err
		}
		// Otherwise (transient/network) fall back to the existing token if it
		// hasn't actually expired yet.
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

// TokenStatus is a read-only view of a stored token's health for reporting in
// the UI. It never triggers a refresh or a network call.
type TokenStatus struct {
	ClientID  string    `json:"client_id"`
	ExpiresAt time.Time `json:"expires_at"`
	// Expired is true when the access token is already past its expiry.
	Expired bool `json:"expired"`
	// Refreshable is true when a refresh token is stored, so an expiring token
	// can be renewed automatically without re-authorization.
	Refreshable bool `json:"refreshable"`
	// NeedsReauth is true when the last automatic refresh was rejected by
	// Trakt — the token is a dead end and the user must re-authorize.
	NeedsReauth bool `json:"needs_reauth"`
	// LastError / LastErrorAt describe the most recent auth failure, when one
	// is outstanding.
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitempty"`
}

// Status returns the stored token's status for clientID, or ok=false if none is
// stored. Purely informational — no refresh, no network.
func Status(bucket tokenBucket, clientID string) (TokenStatus, bool) {
	st, ok := LoadToken(bucket, clientID)
	if !ok {
		return TokenStatus{}, false
	}
	ts := TokenStatus{
		ClientID:    clientID,
		ExpiresAt:   st.ExpiresAt,
		Expired:     !time.Now().Before(st.ExpiresAt),
		Refreshable: st.RefreshToken != "",
	}
	if e, has := loadAuthError(bucket, clientID); has {
		ts.NeedsReauth = true
		ts.LastError = e.Message
		ts.LastErrorAt = e.At
	}
	return ts, true
}
