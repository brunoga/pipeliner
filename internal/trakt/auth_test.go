package trakt

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// memBucket is an in-memory tokenBucket for testing.
type memBucket struct {
	data map[string][]byte
}

func newMemBucket() *memBucket {
	return &memBucket{data: map[string][]byte{}}
}

func (b *memBucket) Put(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	b.data[key] = raw
	return nil
}

func (b *memBucket) Get(key string, dest any) (bool, error) {
	raw, ok := b.data[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, dest)
}

func (b *memBucket) Keys() ([]string, error) {
	keys := make([]string, 0, len(b.data))
	for k := range b.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func TestSaveAndLoadToken(t *testing.T) {
	bucket := newMemBucket()
	tok := &Token{
		AccessToken:  "acc",
		RefreshToken: "ref",
		ExpiresIn:    7776000,
		CreatedAt:    time.Now().Unix(),
	}

	if err := SaveToken(bucket, "client-id", tok); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	st, ok := LoadToken(bucket, "client-id")
	if !ok {
		t.Fatal("LoadToken: expected found=true")
	}
	if st.AccessToken != "acc" {
		t.Errorf("access_token: got %q", st.AccessToken)
	}
	if st.RefreshToken != "ref" {
		t.Errorf("refresh_token: got %q", st.RefreshToken)
	}
}

func TestLoadTokenMissing(t *testing.T) {
	bucket := newMemBucket()
	_, ok := LoadToken(bucket, "missing-client")
	if ok {
		t.Fatal("expected found=false for missing key")
	}
}

func TestGetValidAccessTokenNoToken(t *testing.T) {
	bucket := newMemBucket()
	_, err := GetValidAccessToken(context.Background(), bucket, "client-id", "secret")
	if err == nil {
		t.Fatal("expected error when no token stored")
	}
}

func TestGetValidAccessTokenFresh(t *testing.T) {
	bucket := newMemBucket()
	tok := &Token{
		AccessToken:  "fresh-acc",
		RefreshToken: "fresh-ref",
		ExpiresIn:    7776000, // 90 days
		CreatedAt:    time.Now().Unix(),
	}
	if err := SaveToken(bucket, "cid", tok); err != nil {
		t.Fatal(err)
	}

	got, err := GetValidAccessToken(context.Background(), bucket, "cid", "secret")
	if err != nil {
		t.Fatalf("GetValidAccessToken: %v", err)
	}
	if got != "fresh-acc" {
		t.Errorf("token: got %q, want %q", got, "fresh-acc")
	}
}

func TestGetValidAccessTokenRefreshesExpiring(t *testing.T) {
	// Token that expires in 3 days (below the 7-day threshold).
	expiring := &Token{
		AccessToken:  "old-acc",
		RefreshToken: "old-ref",
		ExpiresIn:    int64((3 * 24 * time.Hour).Seconds()),
		CreatedAt:    time.Now().Unix(),
	}
	bucket := newMemBucket()
	if err := SaveToken(bucket, "cid", expiring); err != nil {
		t.Fatal(err)
	}

	// Mock refresh endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-acc",
			"refresh_token": "new-ref",
			"expires_in":    7776000,
			"created_at":    time.Now().Unix(),
		})
	}))
	defer srv.Close()
	orig := BaseURL
	BaseURL = srv.URL
	defer func() { BaseURL = orig }()

	got, err := GetValidAccessToken(context.Background(), bucket, "cid", "secret")
	if err != nil {
		t.Fatalf("GetValidAccessToken: %v", err)
	}
	if got != "new-acc" {
		t.Errorf("token after refresh: got %q, want %q", got, "new-acc")
	}

	// Verify the new token was persisted.
	st, _ := LoadToken(bucket, "cid")
	if st.AccessToken != "new-acc" {
		t.Errorf("persisted token: got %q", st.AccessToken)
	}
}

func TestGetValidAccessTokenRefreshFailsFallsBack(t *testing.T) {
	// Token expiring soon but not yet expired — refresh fails, should fall back.
	expiring := &Token{
		AccessToken:  "old-acc",
		RefreshToken: "old-ref",
		ExpiresIn:    int64((3 * 24 * time.Hour).Seconds()),
		CreatedAt:    time.Now().Unix(),
	}
	bucket := newMemBucket()
	if err := SaveToken(bucket, "cid", expiring); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srv.Close()
	orig := BaseURL
	BaseURL = srv.URL
	defer func() { BaseURL = orig }()

	got, err := GetValidAccessToken(context.Background(), bucket, "cid", "secret")
	if err != nil {
		t.Fatalf("expected fallback to old token, got error: %v", err)
	}
	if got != "old-acc" {
		t.Errorf("fallback token: got %q, want %q", got, "old-acc")
	}
}

func expiringBucket(t *testing.T) *memBucket {
	t.Helper()
	b := newMemBucket()
	if err := SaveToken(b, "cid", &Token{
		AccessToken:  "old-acc",
		RefreshToken: "old-ref",
		ExpiresIn:    int64((3 * 24 * time.Hour).Seconds()),
		CreatedAt:    time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	return b
}

func withBaseURL(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	orig := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = orig })
}

func TestGetValidAccessTokenRejectedRecordsNeedsReauth(t *testing.T) {
	bucket := expiringBucket(t)
	withBaseURL(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid_grant", http.StatusUnauthorized) // 401 → ErrRefreshRejected
	})

	_, err := GetValidAccessToken(context.Background(), bucket, "cid", "secret")
	if !errors.Is(err, ErrRefreshRejected) {
		t.Fatalf("expected ErrRefreshRejected, got %v", err)
	}
	// The failure must be recorded so the UI can flag it.
	st, ok := Status(bucket, "cid")
	if !ok || !st.NeedsReauth || st.LastError == "" {
		t.Errorf("status should report needs_reauth: %+v ok=%v", st, ok)
	}
}

func TestSaveTokenClearsNeedsReauth(t *testing.T) {
	bucket := expiringBucket(t)
	recordAuthError(bucket, "cid", "boom")
	if st, _ := Status(bucket, "cid"); !st.NeedsReauth {
		t.Fatal("precondition: expected needs_reauth")
	}
	// A successful (re-)auth save clears it.
	if err := SaveToken(bucket, "cid", &Token{AccessToken: "new", RefreshToken: "new",
		ExpiresIn: 7776000, CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	if st, _ := Status(bucket, "cid"); st.NeedsReauth {
		t.Errorf("needs_reauth should be cleared after SaveToken: %+v", st)
	}
}

func TestGetValidAccessTokenSuccessClearsPriorError(t *testing.T) {
	bucket := expiringBucket(t)
	recordAuthError(bucket, "cid", "earlier failure")
	withBaseURL(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-acc", "refresh_token": "new-ref",
			"expires_in": 7776000, "created_at": time.Now().Unix(),
		})
	})
	if _, err := GetValidAccessToken(context.Background(), bucket, "cid", "secret"); err != nil {
		t.Fatal(err)
	}
	if st, _ := Status(bucket, "cid"); st.NeedsReauth {
		t.Errorf("successful refresh should clear the error marker: %+v", st)
	}
}

func TestStatusesSkipsMarkerKeys(t *testing.T) {
	bucket := expiringBucket(t)
	recordAuthError(bucket, "cid", "boom") // adds a "cid\x00err" key
	sts, err := Statuses(bucket)
	if err != nil {
		t.Fatal(err)
	}
	if len(sts) != 1 {
		t.Fatalf("expected exactly one token status (marker key skipped), got %d", len(sts))
	}
	if sts[0].ClientID != "cid" {
		t.Errorf("client id = %q", sts[0].ClientID)
	}
}

func TestStatusRefreshableAndExpired(t *testing.T) {
	bucket := newMemBucket()
	SaveToken(bucket, "cid", &Token{AccessToken: "a", RefreshToken: "r",
		ExpiresIn: -3600, CreatedAt: time.Now().Unix()}) // already expired
	st, ok := Status(bucket, "cid")
	if !ok || !st.Expired || !st.Refreshable {
		t.Errorf("expected expired+refreshable: %+v", st)
	}
}
