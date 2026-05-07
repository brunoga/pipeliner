// Package pushover registers a "pushover" notifier that sends notifications
// via the Pushover API.
package pushover

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/notify"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func init() {
	notify.Register("pushover", notify.Descriptor{
		Factory:  func(cfg map[string]any) (notify.Notifier, error) { return newNotifier(cfg) },
		Validate: validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "user", "pushover"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "token", "pushover"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "pushover", "user", "token", "device", "url")...)
	return errs
}

type pushoverNotifier struct {
	user   string
	token  string
	device string
	url    string
	client *http.Client
}

func newNotifier(cfg map[string]any) (*pushoverNotifier, error) {
	user, _ := cfg["user"].(string)
	token, _ := cfg["token"].(string)
	if user == "" || token == "" {
		return nil, fmt.Errorf("pushover: 'user' and 'token' are required")
	}

	device, _ := cfg["device"].(string)
	apiURL, _ := cfg["url"].(string)
	if apiURL == "" {
		apiURL = "https://api.pushover.net/1/messages.json"
	}

	return &pushoverNotifier{
		user:   user,
		token:  token,
		device: device,
		url:    apiURL,
		client: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (n *pushoverNotifier) Send(ctx context.Context, msg notify.Message) error {
	data := url.Values{}
	data.Set("token", n.token)
	data.Set("user", n.user)
	data.Set("title", msg.Title)
	data.Set("message", msg.Body)
	if n.device != "" {
		data.Set("device", n.device)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, strings.NewReader(data.Encode()))

	if err != nil {
		return fmt.Errorf("pushover: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("pushover: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pushover: server returned %s", resp.Status)
	}
	return nil
}
