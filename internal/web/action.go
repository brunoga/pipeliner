package web

import (
	"fmt"
	"html"
	"net/http"

	"github.com/brunoga/pipeliner/internal/actionlink"
	"github.com/brunoga/pipeliner/internal/ingest"
)

// apiAction serves the one-click links embedded in notifications.
//
// GET renders a confirmation page; POST performs the action. That split is
// not ceremony: mail providers and security scanners routinely prefetch
// links, so a GET that pushed straight into a queue would fire for every
// notification the moment it arrived, without anyone clicking. Authentication
// is the link's own HMAC signature — no session and no bearer header, neither
// of which a mail client can supply.
func (s *Server) apiAction(w http.ResponseWriter, r *http.Request) {
	if !actionlink.Enabled() {
		http.NotFound(w, r)
		return
	}
	enc, sig := r.URL.Query().Get("p"), r.URL.Query().Get("sig")
	p, err := actionlink.Decode(enc, sig)
	if err != nil {
		actionPage(w, http.StatusForbidden, "Link not valid",
			"This link could not be verified. It may have been altered, or the server's signing secret changed.", "")
		return
	}

	label := p.Label
	if label == "" {
		label = "Run " + p.Pipeline
	}

	if r.Method == http.MethodGet {
		actionPage(w, http.StatusOK, label,
			fmt.Sprintf("%s — confirm to send this to the %q pipeline.", p.Title, p.Pipeline),
			fmt.Sprintf(`<form method="post"><input type="hidden" name="p" value="%s"><input type="hidden" name="sig" value="%s"><button type="submit">%s</button></form>`,
				html.EscapeString(enc), html.EscapeString(sig), html.EscapeString(label)))
		return
	}

	item := ingest.Item{Title: p.Title, Fields: map[string]any{}}
	for k, v := range p.Fields {
		item.Fields[k] = v
	}
	accepted, _ := ingest.Enqueue(p.Queue, []ingest.Item{item})
	if accepted == 0 {
		actionPage(w, http.StatusServiceUnavailable, "Queue full",
			"The ingest queue is full; try again after the pipeline drains.", "")
		return
	}
	if s.daemon != nil {
		s.daemon.Trigger(p.Pipeline, false)
	}
	actionPage(w, http.StatusOK, "Done",
		fmt.Sprintf("%s was sent to %q. It will appear once the pipeline finishes.", p.Title, p.Pipeline), "")
}

// actionPage renders a minimal self-contained page. Deliberately standalone:
// it is opened from a mail client by someone who may have no pipeliner
// session, so it cannot depend on the SPA or its assets.
func actionPage(w http.ResponseWriter, code int, heading, detail, form string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s — pipeliner</title>
<style>
 body{font:14px/1.5 system-ui,sans-serif;background:#12151a;color:#e6e6e6;margin:0;
      display:flex;min-height:100vh;align-items:center;justify-content:center;padding:24px}
 .card{max-width:32rem;border:1px solid #2a2f3a;border-radius:8px;padding:24px 28px;background:#171b22}
 h1{font-size:16px;margin:0 0 10px}
 p{color:#9aa4b2;margin:0 0 18px}
 button{font:inherit;padding:8px 16px;border-radius:4px;border:1px solid #3d7dff;
        background:#3d7dff;color:#fff;cursor:pointer}
</style>
<div class="card"><h1>%s</h1><p>%s</p>%s</div>`,
		html.EscapeString(heading), html.EscapeString(heading), html.EscapeString(detail), form)
}
