// Package webhook registers a "webhook" notifier that POSTs a JSON payload
// to a configured URL.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/brunoga/pipeliner/internal/notify"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func init() {
	notify.Register("webhook", func(cfg map[string]any) (notify.Notifier, error) {
		return newNotifier(cfg)
	})
}

// Validate checks the webhook notifier configuration.
func Validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "url", "webhook"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "webhook", "url", "headers")...)
	return errs
}

type webhookNotifier struct {
	url     string
	headers map[string]string
	client  *http.Client
}

func newNotifier(cfg map[string]any) (*webhookNotifier, error) {
	u, _ := cfg["url"].(string)
	if u == "" {
		return nil, fmt.Errorf("webhook: 'url' is required")
	}

	headers := map[string]string{}
	if h, ok := cfg["headers"].(map[string]any); ok {
		for k, v := range h {
			if s, ok := v.(string); ok {
				headers[k] = s
			}
		}
	}

	return &webhookNotifier{
		url:     u,
		headers: headers,
		client:  &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// payload is the JSON body sent to the webhook endpoint.
type payload struct {
	Title   string         `json:"title"`
	Body    string         `json:"body"`
	Entries []entryPayload `json:"entries"`
}

type entryPayload struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

func (n *webhookNotifier) Send(ctx context.Context, msg notify.Message) error {
	eps := make([]entryPayload, len(msg.Entries))
	for i, e := range msg.Entries {
		eps[i] = entryPayload{Title: e.Title, URL: e.URL}
	}

	body, err := json.Marshal(payload{
		Title:   msg.Title,
		Body:    msg.Body,
		Entries: eps,
	})
	if err != nil {
		return fmt.Errorf("webhook: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range n.headers {
		req.Header.Set(k, v)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: server returned %s", resp.Status)
	}
	return nil
}
