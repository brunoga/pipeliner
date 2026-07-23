// Package notify provides an output plugin that dispatches notifications via
// a configured notifier (e.g. "webhook", "email").
package notify

import (
	"bytes"
	"context"
	"fmt"
	"text/template"
	"time"

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
		// Notify is the canonical destination for report_empty's marker
		// (empty-batch alerts) — opt in so the executor doesn't strip
		// the marker before Consume sees it. Notify is a pure messenger;
		// passing a marker through it just sends a notification, which
		// is exactly what the user asked for.
		AcceptsMarkers: true,
		Factory:        newPlugin,
		Validate:       validate,
		Schema: []plugin.FieldSchema{
			{Key: "via", Type: plugin.FieldTypeString, Required: true, Hint: "Notifier type (e.g. webhook, email)"},
			{Key: "title", Type: plugin.FieldTypePattern, Hint: "Notification title template"},
			{Key: "body", Type: plugin.FieldTypePattern, Hint: "Notification body template", Multiline: true},
			{Key: "digest", Type: plugin.FieldTypeDuration, Hint: "Digest window (e.g. 24h): buffer entries across runs and send one combined notification per window"},
			{Key: "on", Type: plugin.FieldTypeEnum, Enum: []string{"only-accepted", "all"}, Hint: "When to send (default only-accepted)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "via", "notify"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "notify", "via", "config", "title", "body", "on", "digest")...)
	if d, ok := cfg["digest"].(string); ok && d != "" {
		if _, err := time.ParseDuration(d); err != nil {
			errs = append(errs, fmt.Errorf("notify: invalid digest window: %v", err))
		}
	}

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

	// digest, when non-zero, switches to cross-run digest mode: entries are
	// buffered in the store and one combined notification is sent when the
	// window has elapsed since the last send. Buffered entries carry title,
	// URL and accept reason only — digest body templates should limit
	// themselves to those.
	digest time.Duration
	bucket store.Bucket
	now    func() time.Time
}

// digestItem is the persisted form of one buffered entry.
type digestItem struct {
	Title  string    `json:"title"`
	URL    string    `json:"url"`
	Reason string    `json:"reason,omitempty"`
	At     time.Time `json:"at"`
}

// digestState tracks the buffer and the last flush time.
type digestState struct {
	LastFlush time.Time    `json:"last_flush"`
	Items     []digestItem `json:"items"`
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
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

	var digest time.Duration
	var bucket store.Bucket
	if d, ok := cfg["digest"].(string); ok && d != "" {
		dd, err := time.ParseDuration(d)
		if err != nil {
			return nil, fmt.Errorf("notify: invalid digest window: %w", err)
		}
		if db == nil {
			return nil, fmt.Errorf("notify: digest mode requires the store")
		}
		digest = dd
		bucket = db.Bucket("notify_digest")
	}

	return &notifyPlugin{
		notifier:  n,
		titleTmpl: titleTmpl,
		bodyTmpl:  bodyTmpl,
		onAll:     onVal == "all",
		digest:    digest,
		bucket:    bucket,
		now:       time.Now,
	}, nil
}

func (p *notifyPlugin) Name() string { return "notify" }

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
	if p.digest > 0 {
		return p.consumeDigest(ctx, tc, entries)
	}
	return p.deliver(ctx, tc, entries)
}

// digestKey scopes the buffer per task so two pipelines with digest notify
// sinks don't mix their items.
func digestKey(tc *plugin.TaskContext) string { return tc.Name }

// consumeDigest buffers this run's entries and sends one combined
// notification when the digest window has elapsed since the last send.
func (p *notifyPlugin) consumeDigest(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	var st digestState
	if _, err := p.bucket.Get(digestKey(tc), &st); err != nil {
		return fmt.Errorf("notify: read digest state: %w", err)
	}
	for _, e := range entries {
		st.Items = append(st.Items, digestItem{
			Title: e.Title, URL: e.URL, Reason: e.AcceptReason, At: p.now(),
		})
	}
	if st.LastFlush.IsZero() {
		// First ever run: start the window now instead of sending a
		// one-item "digest" immediately.
		st.LastFlush = p.now()
		return p.bucket.Put(digestKey(tc), st)
	}
	if p.now().Sub(st.LastFlush) < p.digest {
		return p.bucket.Put(digestKey(tc), st)
	}
	if len(st.Items) == 0 && !p.onAll {
		// Window elapsed with nothing collected: just restart it.
		st.LastFlush = p.now()
		return p.bucket.Put(digestKey(tc), st)
	}

	// Reconstruct lightweight entries so the regular templates work.
	buffered := make([]*entry.Entry, 0, len(st.Items))
	for _, it := range st.Items {
		e := entry.New(it.Title, it.URL)
		if it.Reason != "" {
			e.Accept(it.Reason)
		}
		buffered = append(buffered, e)
	}
	if err := p.deliver(ctx, tc, buffered); err != nil {
		// Keep the buffer so a transient notifier failure loses nothing;
		// the next elapsed window retries.
		if perr := p.bucket.Put(digestKey(tc), st); perr != nil {
			tc.Logger.Warn("notify: persist digest after send failure", "err", perr)
		}
		return err
	}
	st.Items = nil
	st.LastFlush = p.now()
	return p.bucket.Put(digestKey(tc), st)
}
