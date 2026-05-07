package notify

import (
	"context"
	"testing"
)

func TestRegisterAndLookup(t *testing.T) {
	called := false
	Register("test-notifier", Descriptor{
		Factory: func(_ map[string]any) (Notifier, error) {
			return &mockNotifier{&called}, nil
		},
	})

	d, ok := Lookup("test-notifier")
	if !ok {
		t.Fatal("expected registered notifier to be found")
	}
	n, err := d.Factory(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Send(context.Background(), Message{Title: "hi"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("expected Send to be called")
	}
}

func TestLookupMissing(t *testing.T) {
	_, ok := Lookup("no-such-notifier")
	if ok {
		t.Error("expected false for unknown notifier")
	}
}

func TestDuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	Register("dup-notifier", Descriptor{Factory: func(_ map[string]any) (Notifier, error) { return nil, nil }})
	Register("dup-notifier", Descriptor{Factory: func(_ map[string]any) (Notifier, error) { return nil, nil }})
}

type mockNotifier struct{ called *bool }

func (m *mockNotifier) Send(_ context.Context, _ Message) error {
	*m.called = true
	return nil
}
