// Package email registers an "email" notifier that sends SMTP notifications.
package email

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/brunoga/pipeliner/internal/notify"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func init() {
	notify.Register("email", notify.Descriptor{
		Factory:  func(cfg map[string]any) (notify.Notifier, error) { return newNotifier(cfg) },
		Validate: validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "smtp_host", "email"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "from", "email"); err != nil {
		errs = append(errs, err)
	}
	to := toStringSlice(cfg["to"])
	if len(to) == 0 {
		errs = append(errs, fmt.Errorf("email: \"to\" list must be non-empty"))
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "email", "smtp_host", "smtp_port", "from", "to", "username", "password", "html")...)
	return errs
}

type emailNotifier struct {
	host     string
	port     int
	username string
	password string
	from     string
	to       []string
	html     bool
}

func newNotifier(cfg map[string]any) (*emailNotifier, error) {
	host, _ := cfg["smtp_host"].(string)
	if host == "" {
		return nil, fmt.Errorf("email notifier: 'smtp_host' is required")
	}
	from, _ := cfg["from"].(string)
	if from == "" {
		return nil, fmt.Errorf("email notifier: 'from' is required")
	}
	to := toStringSlice(cfg["to"])
	if len(to) == 0 {
		return nil, fmt.Errorf("email notifier: 'to' is required")
	}

	port := intVal(cfg["smtp_port"], 25)
	username, _ := cfg["username"].(string)
	password, _ := cfg["password"].(string)
	html, _ := cfg["html"].(bool)

	return &emailNotifier{
		host:     host,
		port:     port,
		username: username,
		password: password,
		from:     from,
		to:       to,
		html:     html,
	}, nil
}

func (n *emailNotifier) Send(_ context.Context, msg notify.Message) error {
	addr := fmt.Sprintf("%s:%d", n.host, n.port)

	var auth smtp.Auth
	if n.username != "" {
		auth = smtp.PlainAuth("", n.username, n.password, n.host)
	}

	body := buildMessage(n.from, n.to, msg.Title, msg.Body, n.html)
	return smtp.SendMail(addr, auth, n.from, n.to, body)
}

func buildMessage(from string, to []string, subject, body string, html bool) []byte {
	ct := "text/plain; charset=utf-8"
	if html {
		ct = "text/html; charset=utf-8"
	}
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: " + ct + "\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return []byte(sb.String())
}

func toStringSlice(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func intVal(v any, def int) int {
	switch t := v.(type) {
	case int:
		return t
	case float64:
		return int(t)
	}
	return def
}
