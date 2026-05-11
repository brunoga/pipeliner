// Package notify provides an output plugin that dispatches notifications via
// a configured notifier (e.g. "webhook", "email").
package notify

import (
	"bytes"
	"context"
	"fmt"
	"text/template"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/notify"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	itpl "github.com/brunoga/pipeliner/internal/template"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "notify",
		Description: "send notifications via a configured notifier (webhook, email, …)",
		Role:        plugin.RoleSink,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "via", "notify"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "notify", "via", "config", "title", "body", "on")...)

	via, _ := cfg["via"].(string)
	if via != "" {
		desc, ok := notify.Lookup(via)
		if !ok {
			errs = append(errs, fmt.Errorf("notify: unknown notifier %q", via))
		} else if desc.Validate != nil {
			notifierCfg, _ := cfg["config"].(map[string]any)
			if notifierCfg == nil {
				notifierCfg = map[string]any{}
			}
			errs = append(errs, desc.Validate(notifierCfg)...)
		}
	}
	return errs
}

type notifyPlugin struct {
	notifier  notify.Notifier
	titleTmpl *template.Template
	bodyTmpl  *template.Template
	onAll     bool // if true, notify even when no entries were accepted
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	via, _ := cfg["via"].(string)
	if via == "" {
		return nil, fmt.Errorf("notify: 'via' is required (e.g. \"webhook\", \"email\")")
	}

	desc, ok := notify.Lookup(via)
	if !ok {
		return nil, fmt.Errorf("notify: unknown notifier %q", via)
	}

	// Pass the entire config to the notifier factory; it picks what it needs.
	notifierCfg, _ := cfg["config"].(map[string]any)
	if notifierCfg == nil {
		notifierCfg = map[string]any{}
	}
	n, err := desc.Factory(notifierCfg)
	if err != nil {
		return nil, fmt.Errorf("notify: create %q notifier: %w", via, err)
	}

	titleStr, _ := cfg["title"].(string)
	if titleStr == "" {
		titleStr = "pipeliner: {{len .Entries}} new item(s)"
	}
	titleTmpl, err := template.New("title").Funcs(itpl.FuncMap()).Parse(titleStr)
	if err != nil {
		return nil, fmt.Errorf("notify: invalid title template: %w", err)
	}

	bodyStr, _ := cfg["body"].(string)
	if bodyStr == "" {
		bodyStr = "{{range .Entries}}- {{.Title}}\n{{end}}"
	}
	bodyTmpl, err := template.New("body").Funcs(itpl.FuncMap()).Parse(bodyStr)
	if err != nil {
		return nil, fmt.Errorf("notify: invalid body template: %w", err)
	}

	onVal, _ := cfg["on"].(string)

	return &notifyPlugin{
		notifier:  n,
		titleTmpl: titleTmpl,
		bodyTmpl:  bodyTmpl,
		onAll:     onVal == "all",
	}, nil
}

func (p *notifyPlugin) Name() string        { return "notify" }

func (p *notifyPlugin) deliver(ctx context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	if len(entries) == 0 && !p.onAll {
		return nil
	}

	data := map[string]any{"Entries": entries}

	title, err := renderTmpl(p.titleTmpl, data)
	if err != nil {
		return fmt.Errorf("notify: render title: %w", err)
	}
	body, err := renderTmpl(p.bodyTmpl, data)
	if err != nil {
		return fmt.Errorf("notify: render body: %w", err)
	}

	return p.notifier.Send(ctx, notify.Message{
		Title:   title,
		Body:    body,
		Entries: entries,
	})
}

func renderTmpl(tmpl *template.Template, data map[string]any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}


func (p *notifyPlugin) Consume(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if tc.DryRun {
		return nil
	}
	return p.deliver(ctx, tc, entry.FilterAccepted(entries))
}
