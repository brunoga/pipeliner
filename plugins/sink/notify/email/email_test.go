package email

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/notify"
)

type mockSMTP struct {
	ln    net.Listener
	msgCh chan string
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
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			send("250 OK")
		case strings.HasPrefix(upper, "MAIL FROM"):
			send("250 OK")
		case strings.HasPrefix(upper, "RCPT TO"):
			send("250 OK")
		case upper == "DATA":
			send("354 Start input")
			inData = true
		case inData && line == ".":
			send("250 OK")
			m.msgCh <- body.String()
			send("221 Bye")
			return
		case inData:
			body.WriteString(line + "\n")
		case upper == "QUIT":
			send("221 Bye")
			return
		}
	}
}

func makeNotifier(t *testing.T, srv *mockSMTP) *emailNotifier {
	t.Helper()
	host, portStr, _ := net.SplitHostPort(srv.addr())
	port := 0
	for _, b := range portStr {
		port = port*10 + int(b-'0')
	}
	return &emailNotifier{
		host: host,
		port: port,
		from: "from@example.com",
		to:   []string{"to@example.com"},
	}
}

func TestEmailSend(t *testing.T) {
	srv := newMockSMTP(t)
	defer srv.ln.Close()

	n := makeNotifier(t, srv)
	e := entry.New("Test Entry", "http://example.com")
	msg := notify.Message{
		Title:   "Test Subject",
		Body:    "Test body text",
		Entries: []*entry.Entry{e},
	}

	if err := n.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	transcript := <-srv.msgCh
	if !strings.Contains(transcript, "Test Subject") {
		t.Errorf("subject not found in SMTP body:\n%s", transcript)
	}
}

func TestMissingHost(t *testing.T) {
	_, err := newNotifier(map[string]any{"sender": "a@b.com", "to": "x@y.com"})
	if err == nil {
		t.Fatal("expected error when smtp_host is missing")
	}
}

func TestMissingFrom(t *testing.T) {
	_, err := newNotifier(map[string]any{"smtp_host": "localhost", "to": "x@y.com"})
	if err == nil {
		t.Fatal("expected error when from is missing")
	}
}

func TestMissingTo(t *testing.T) {
	_, err := newNotifier(map[string]any{"smtp_host": "localhost", "sender": "a@b.com"})
	if err == nil {
		t.Fatal("expected error when to is missing")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := notify.Lookup("email")
	if !ok {
		t.Fatal("email notifier not registered")
	}
	_, err := d.Factory(map[string]any{
		"smtp_host": "localhost",
		"sender":      "a@b.com",
		"to":        "x@y.com",
	})
	if err != nil {
		t.Fatal(err)
	}
}
