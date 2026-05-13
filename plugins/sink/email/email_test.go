package email

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.Default()}
}

// mockSMTP listens on a random port, accepts one connection, records the DATA
// section, and shuts down.
type mockSMTP struct {
	ln      net.Listener
	msgCh   chan string
}

func newMockSMTP(t *testing.T) *mockSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &mockSMTP{ln: ln, msgCh: make(chan string, 1)}
	go m.serve()
	return m
}

func (m *mockSMTP) addr() string { return m.ln.Addr().String() }

func (m *mockSMTP) serve() {
	conn, err := m.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	w := bufio.NewWriter(conn)
	r := bufio.NewReader(conn)

	send := func(s string) { w.WriteString(s + "\r\n"); w.Flush() } //nolint:errcheck

	send("220 mock SMTP ready")
	var body strings.Builder
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(strings.ToUpper(line), "EHLO"), strings.HasPrefix(strings.ToUpper(line), "HELO"):
			send("250 OK")
		case strings.HasPrefix(strings.ToUpper(line), "MAIL FROM"):
			send("250 OK")
		case strings.HasPrefix(strings.ToUpper(line), "RCPT TO"):
			send("250 OK")
		case strings.ToUpper(line) == "DATA":
			send("354 Start input")
			inData = true
		case inData && line == ".":
			send("250 OK")
			m.msgCh <- body.String()
			send("221 Bye")
			return
		case inData:
			body.WriteString(line + "\n")
		case strings.ToUpper(line) == "QUIT":
			send("221 Bye")
			return
		}
	}
}

func pluginCfg(addr string) map[string]any {
	host, port, _ := net.SplitHostPort(addr)
	portInt := 25
	if port != "" {
		for _, b := range port {
			portInt = portInt*10 + int(b-'0')
			portInt -= 25 // reset accumulation
		}
		// simpler: parse directly
		portInt = 0
		for _, b := range port {
			portInt = portInt*10 + int(b-'0')
		}
	}
	return map[string]any{
		"smtp_host": host,
		"smtp_port": portInt,
		"sender":      "test@example.com",
		"to":        "dest@example.com",
	}
}

func TestSendsEmail(t *testing.T) {
	srv := newMockSMTP(t)
	defer srv.ln.Close()

	cfg := pluginCfg(srv.addr())
	p, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	e := entry.New("My Show S01E01", "http://x.com/a")
	if err := p.(*emailPlugin).deliver(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatalf("Output: %v", err)
	}

	msg := <-srv.msgCh
	if !strings.Contains(msg, "My Show S01E01") {
		t.Errorf("email body should contain entry title; got:\n%s", msg)
	}
	if !strings.Contains(msg, "From: test@example.com") {
		t.Errorf("email should have From header; got:\n%s", msg)
	}
}

func TestSubjectTemplate(t *testing.T) {
	srv := newMockSMTP(t)
	defer srv.ln.Close()

	cfg := pluginCfg(srv.addr())
	cfg["subject"] = "New: {{(index .Entries 0).Title}}"
	p, _ := newPlugin(cfg, nil)

	e := entry.New("Cool Episode", "http://x.com/a")
	p.(*emailPlugin).deliver(context.Background(), makeCtx(), []*entry.Entry{e}) //nolint:errcheck

	msg := <-srv.msgCh
	if !strings.Contains(msg, "Subject: New: Cool Episode") {
		t.Errorf("subject not set correctly; got:\n%s", msg)
	}
}

func TestNoEntriesSkipsSend(t *testing.T) {
	// No mock server — send must not be called.
	p, _ := newPlugin(map[string]any{
		"smtp_host": "127.0.0.1",
		"smtp_port": 1, // would fail if called
		"sender":      "a@b.com",
		"to":        "b@c.com",
	}, nil)
	err := p.(*emailPlugin).deliver(context.Background(), makeCtx(), nil)
	if err != nil {
		t.Errorf("no entries: unexpected error: %v", err)
	}
}

func TestMissingHost(t *testing.T) {
	_, err := newPlugin(map[string]any{"sender": "a@b.com", "to": "b@c.com"}, nil)
	if err == nil {
		t.Error("expected error when smtp_host missing")
	}
}

func TestMissingFrom(t *testing.T) {
	_, err := newPlugin(map[string]any{"smtp_host": "localhost", "to": "b@c.com"}, nil)
	if err == nil {
		t.Error("expected error when from missing")
	}
}

func TestMissingTo(t *testing.T) {
	_, err := newPlugin(map[string]any{"smtp_host": "localhost", "sender": "a@b.com"}, nil)
	if err == nil {
		t.Error("expected error when to missing")
	}
}

func TestBuildMessage(t *testing.T) {
	msg := buildMessage("a@b.com", []string{"x@y.com", "z@w.com"}, "Hello", "body text", false)
	s := string(msg)
	if !strings.Contains(s, "From: a@b.com") {
		t.Error("missing From header")
	}
	if !strings.Contains(s, "To: x@y.com, z@w.com") {
		t.Error("missing To header")
	}
	if !strings.Contains(s, "Subject: Hello") {
		t.Error("missing Subject header")
	}
	if !strings.Contains(s, "body text") {
		t.Error("missing body")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("email")
	if !ok {
		t.Fatal("email plugin not registered")
	}
	if d.Role != plugin.RoleSink {
		t.Errorf("phase: got %v", d.Role)
	}
}
