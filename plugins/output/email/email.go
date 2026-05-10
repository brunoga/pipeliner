// Package email sends a batch email for all accepted entries via SMTP.
package email

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"net/smtp"
	"strings"
	"text/template"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	itpl "github.com/brunoga/pipeliner/internal/template"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "email",
		Description: "send a batch email for all accepted entries via SMTP",
		PluginPhase: plugin.PhaseOutput,
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "smtp_host", Type: plugin.FieldTypeString, Required: true, Hint: "SMTP server hostname"},
			{Key: "smtp_port", Type: plugin.FieldTypeInt, Default: 25, Hint: "SMTP server port"},
			{Key: "from", Type: plugin.FieldTypeString, Required: true, Hint: "Sender address"},
			{Key: "to", Type: plugin.FieldTypeList, Required: true, Hint: "Recipient address(es)"},
			{Key: "username", Type: plugin.FieldTypeString, Hint: "SMTP auth username"},
			{Key: "password", Type: plugin.FieldTypeString, Hint: "SMTP auth password"},
			{Key: "subject", Type: plugin.FieldTypeString, Hint: "Email subject Go template (default: pipeliner: N new item(s))"},
			{Key: "body_template", Type: plugin.FieldTypeString, Hint: "Email body Go template"},
			{Key: "html", Type: plugin.FieldTypeBool, Hint: "Send HTML email instead of plain text"},
		},
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
	errs = append(errs, plugin.OptUnknownKeys(cfg, "email", "smtp_host", "smtp_port", "from", "to", "username", "password", "subject", "body_template", "html")...)
	return errs
}

type emailPlugin struct {
	host        string
	port        int
	username    string
	password    string
	from        string
	to          []string
	html        bool
	subjectTmpl *template.Template
	bodyTmpl    *template.Template
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	host, _ := cfg["smtp_host"].(string)
	if host == "" {
		return nil, fmt.Errorf("email: 'smtp_host' is required")
	}
	port := 25
	if p, ok := cfg["smtp_port"].(int); ok && p > 0 {
		port = p
	} else if p, ok := cfg["smtp_port"].(float64); ok && p > 0 {
		port = int(p)
	}

	from, _ := cfg["from"].(string)
	if from == "" {
		return nil, fmt.Errorf("email: 'from' is required")
	}

	to := toStringSlice(cfg["to"])
	if len(to) == 0 {
		return nil, fmt.Errorf("email: 'to' is required")
	}

	subjectPat, _ := cfg["subject"].(string)
	if subjectPat == "" {
		subjectPat = "pipeliner: {{len .Entries}} new item(s)"
	}
	subjectTmpl, err := template.New("subject").Funcs(itpl.FuncMap()).Parse(subjectPat)
	if err != nil {
		return nil, fmt.Errorf("email: invalid subject template: %w", err)
	}

	html, _ := cfg["html"].(bool)

	defaultBody := "{{range .Entries}}- {{.Title}}\n  {{.URL}}\n{{end}}"
	if html {
		defaultBody = "<ul>\n{{range .Entries}}<li><a href=\"{{.URL}}\">{{.Title}}</a></li>\n{{end}}</ul>"
	}
	bodyPat, _ := cfg["body_template"].(string)
	if bodyPat == "" {
		bodyPat = defaultBody
	}
	bodyTmpl, err := template.New("body").Funcs(itpl.FuncMap()).Parse(bodyPat)
	if err != nil {
		return nil, fmt.Errorf("email: invalid body template: %w", err)
	}

	return &emailPlugin{
		host:        host,
		port:        port,
		username:    stringVal(cfg["username"]),
		password:    stringVal(cfg["password"]),
		from:        from,
		to:          to,
		html:        html,
		subjectTmpl: subjectTmpl,
		bodyTmpl:    bodyTmpl,
	}, nil
}

func (p *emailPlugin) Name() string        { return "email" }
func (p *emailPlugin) Phase() plugin.Phase { return plugin.PhaseOutput }

func (p *emailPlugin) Output(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	if len(entries) == 0 {
		return nil
	}

	data := map[string]any{
		"Entries": entries,
		"Count":   len(entries),
		"Time":    time.Now(),
	}
	// Expose fields of first entry at top level for simple single-entry templates.
	if len(entries) == 1 {
		m := map[string]any{
			"Title": entries[0].Title,
			"URL":   entries[0].URL,
		}
		maps.Copy(m, entries[0].Fields)
		maps.Copy(data, m)
	}

	subject, err := render(p.subjectTmpl, data)
	if err != nil {
		return fmt.Errorf("email: render subject: %w", err)
	}
	body, err := render(p.bodyTmpl, data)
	if err != nil {
		return fmt.Errorf("email: render body: %w", err)
	}

	return p.send(subject, body)
}

func (p *emailPlugin) send(subject, body string) error {
	addr := fmt.Sprintf("%s:%d", p.host, p.port)

	var auth smtp.Auth
	if p.username != "" {
		auth = smtp.PlainAuth("", p.username, p.password, p.host)
	}

	msg := buildMessage(p.from, p.to, subject, body, p.html)
	return smtp.SendMail(addr, auth, p.from, p.to, msg)
}

func buildMessage(from string, to []string, subject, body string, html bool) []byte {
	ct := "text/plain; charset=utf-8"
	if html {
		ct = "text/html; charset=utf-8"
	}
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: " + ct + "\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}

func render(tmpl *template.Template, data map[string]any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func stringVal(v any) string {
	s, _ := v.(string)
	return s
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
