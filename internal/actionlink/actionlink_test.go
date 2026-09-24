package actionlink

import (
	"net/url"
	"strings"
	"testing"
)

func configured(t *testing.T) {
	t.Helper()
	Configure("https://pipeliner.example.com/", "test-secret")
	t.Cleanup(func() { Configure("", "") })
}

func TestRoundTrip(t *testing.T) {
	configured(t)
	want := Payload{
		Queue: "favorites", Pipeline: "tvshows-favorite-add",
		Title: "Some Show", Fields: map[string]string{"tvdb_id": "12345"}, Label: "⭐ Follow",
	}
	raw, err := URL(want)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/action" || u.Host != "pipeliner.example.com" {
		t.Fatalf("url = %s", raw)
	}
	got, err := Decode(u.Query().Get("p"), u.Query().Get("sig"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Queue != want.Queue || got.Pipeline != want.Pipeline ||
		got.Title != want.Title || got.Fields["tvdb_id"] != "12345" {
		t.Errorf("round trip lost data: %+v", got)
	}
}

// The signature is what makes a leaked link safe: it authorises exactly the
// action it was minted for and cannot be edited into a different one.
func TestTamperedPayloadRejected(t *testing.T) {
	configured(t)
	raw, _ := URL(Payload{Queue: "favorites", Pipeline: "p", Title: "Show", Fields: map[string]string{"tvdb_id": "1"}})
	u, _ := url.Parse(raw)
	p, sig := u.Query().Get("p"), u.Query().Get("sig")

	// Re-point the same signature at a different show.
	evil, _ := URL(Payload{Queue: "favorites", Pipeline: "p", Title: "Other", Fields: map[string]string{"tvdb_id": "999"}})
	evilP := mustQuery(t, evil, "p")
	if _, err := Decode(evilP, sig); err == nil {
		t.Error("a signature must not validate a different payload")
	}
	// Flip the signature.
	if _, err := Decode(p, strings.Repeat("0", len(sig))); err == nil {
		t.Error("a forged signature must be rejected")
	}
	// A different secret must not validate links minted under this one.
	Configure("https://pipeliner.example.com", "other-secret")
	if _, err := Decode(p, sig); err == nil {
		t.Error("links must not verify under a different secret")
	}
}

// Unconfigured installs mint nothing rather than emitting a dead URL, so a
// template carrying a link stays valid everywhere.
func TestDisabledWhenUnconfigured(t *testing.T) {
	Configure("", "")
	if Enabled() {
		t.Fatal("must be disabled with no config")
	}
	got, err := URL(Payload{Queue: "q", Pipeline: "p", Title: "t"})
	if err != nil || got != "" {
		t.Errorf("unconfigured URL = %q, err = %v; want empty", got, err)
	}
	if _, err := Decode("x", "y"); err == nil {
		t.Error("decoding must fail when unconfigured")
	}
}

func TestRejectsIncompletePayload(t *testing.T) {
	configured(t)
	if _, err := URL(Payload{Title: "no queue or pipeline"}); err == nil {
		t.Error("queue and pipeline are required")
	}
}

func mustQuery(t *testing.T, raw, key string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}
