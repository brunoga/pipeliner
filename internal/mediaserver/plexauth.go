// Plex PIN ("link") authentication: the flow real Plex apps use. Start a PIN,
// send the user to plex.tv to approve it, poll until plex.tv attaches an auth
// token. No credentials pass through pipeliner and 2FA works unchanged. The
// token is bound to plexClientID, the same identifier discovery sends, so it
// works for the resources/library calls that follow.
package mediaserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// PlexPin is one in-flight sign-in attempt.
type PlexPin struct {
	ID   int    `json:"id"`
	Code string `json:"code"`
	// AuthURL is the plex.tv page where the user approves this PIN.
	AuthURL string `json:"auth_url"`
}

func plexTVHeaders() http.Header {
	return http.Header{
		"Accept":                   {"application/json"},
		"X-Plex-Product":           {"pipeliner"},
		"X-Plex-Client-Identifier": {plexClientID},
	}
}

// StartPlexPin creates a PIN on plex.tv and returns the approval URL.
func StartPlexPin(ctx context.Context) (*PlexPin, error) {
	hc := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		PlexTVBaseURL+"/api/v2/pins?strong=true", nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range plexTVHeaders() {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plex.tv pins: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("plex.tv pins: status %d", resp.StatusCode)
	}
	var pin struct {
		ID   int    `json:"id"`
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pin); err != nil {
		return nil, fmt.Errorf("plex.tv pins: %w", err)
	}
	// The hosted approval page; after the user clicks "Allow" there, plex.tv
	// attaches the auth token to the PIN and CheckPlexPin picks it up.
	authURL := "https://app.plex.tv/auth#?" + url.Values{
		"clientID":                 {plexClientID},
		"code":                     {pin.Code},
		"context[device][product]": {"pipeliner"},
	}.Encode()
	return &PlexPin{ID: pin.ID, Code: pin.Code, AuthURL: authURL}, nil
}

// CheckPlexPin returns the auth token for a PIN, or "" while the user has not
// yet approved it. An error means the PIN is invalid or expired.
func CheckPlexPin(ctx context.Context, id int) (string, error) {
	hc := &http.Client{Timeout: 15 * time.Second}
	var out struct {
		AuthToken string `json:"authToken"`
	}
	if err := getJSON(ctx, hc, fmt.Sprintf("%s/api/v2/pins/%d", PlexTVBaseURL, id), plexTVHeaders(), &out); err != nil {
		return "", fmt.Errorf("plex.tv pin %d: %w", id, err)
	}
	return out.AuthToken, nil
}
