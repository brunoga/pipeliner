package clog

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// colorHandler builds a Handler with color forced on, bypassing TTY detection.
func colorHandler(buf *bytes.Buffer, level slog.Level) *Handler {
	return &Handler{
		w:     buf,
		color: true,
		opts:  slog.HandlerOptions{Level: level},
	}
}

// --- plain (no-color) output ---

func TestHandlerPlainNoANSI(t *testing.T) {
	var buf bytes.Buffer
	// bytes.Buffer is not *os.File, so New always picks color=false.
	h := New(&buf, nil)
	slog.New(h).Info("task started", "task", "foo")

	out := buf.String()
	if strings.Contains(out, "\033[") {
		t.Errorf("unexpected ANSI codes in plain output: %q", out)
	}
	if !strings.Contains(out, "task started") {
		t.Errorf("missing message: %q", out)
	}
	if !strings.Contains(out, "task=foo") {
		t.Errorf("missing attr: %q", out)
	}
}

func TestHandlerTimestamp(t *testing.T) {
	var buf bytes.Buffer
	h := New(&buf, nil)

	r := slog.NewRecord(time.Date(2024, 1, 15, 9, 5, 3, 0, time.UTC), slog.LevelInfo, "hello", 0)
	_ = h.Handle(context.Background(), r)

	if !strings.HasPrefix(buf.String(), "2024-01-15 09:05:03.000") {
		t.Errorf("unexpected output prefix: %q", buf.String())
	}
}

func TestHandlerZeroTimeOmitted(t *testing.T) {
	var buf bytes.Buffer
	h := New(&buf, nil)

	r := slog.NewRecord(time.Time{}, slog.LevelInfo, "hello", 0)
	_ = h.Handle(context.Background(), r)

	// With no time, output should start with the level.
	if !strings.HasPrefix(buf.String(), "INFO") {
		t.Errorf("zero time: expected level at start, got: %q", buf.String())
	}
}

// --- level filtering ---

func TestHandlerEnabled(t *testing.T) {
	h := New(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})
	ctx := context.Background()

	if h.Enabled(ctx, slog.LevelInfo) {
		t.Error("INFO should not be enabled at WARN threshold")
	}
	if !h.Enabled(ctx, slog.LevelWarn) {
		t.Error("WARN should be enabled at WARN threshold")
	}
	if !h.Enabled(ctx, slog.LevelError) {
		t.Error("ERROR should be enabled at WARN threshold")
	}
}

func TestHandlerLevelFiltersOutput(t *testing.T) {
	var buf bytes.Buffer
	h := New(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	l := slog.New(h)

	l.Debug("should be dropped")
	l.Info("should appear")

	out := buf.String()
	if strings.Contains(out, "should be dropped") {
		t.Errorf("debug message passed INFO filter: %q", out)
	}
	if !strings.Contains(out, "should appear") {
		t.Errorf("info message missing from output: %q", out)
	}
}

// --- color: levels ---

func TestHandlerLevelColors(t *testing.T) {
	tests := []struct {
		level    slog.Level
		wantANSI string
	}{
		{slog.LevelDebug, ansiGray},
		{slog.LevelInfo, ansiCyan},
		{slog.LevelWarn, ansiYellow},
		{slog.LevelError, ansiRed},
	}

	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			var buf bytes.Buffer
			h := colorHandler(&buf, slog.LevelDebug)
			slog.New(h).Log(context.Background(), tt.level, "msg")

			if !strings.Contains(buf.String(), tt.wantANSI) {
				t.Errorf("want %q in output for level %v, got: %q", tt.wantANSI, tt.level, buf.String())
			}
		})
	}
}

func TestHandlerInfoLevelColor(t *testing.T) {
	var buf bytes.Buffer
	h := colorHandler(&buf, slog.LevelInfo)
	slog.New(h).Info("plain info message")

	out := buf.String()
	if !strings.Contains(out, ansiCyan) {
		t.Errorf("INFO level: expected cyan in output: %q", out)
	}
}

// --- color: entry state messages ---

func TestHandlerEntryStateColors(t *testing.T) {
	tests := []struct {
		msg      string
		wantANSI string
	}{
		{"entry accepted", ansiGreen},
		{"entry rejected", ansiRed},
		{"entry undecided", ansiYellow},
		{"entry failed", ansiMagenta},
	}

	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			var buf bytes.Buffer
			h := colorHandler(&buf, slog.LevelInfo)
			// "entry failed" is logged at Warn in production, but msgColor fires on
			// the message string regardless of level.
			slog.New(h).Log(context.Background(), slog.LevelInfo, tt.msg)

			if !strings.Contains(buf.String(), tt.wantANSI) {
				t.Errorf("msg %q: want ANSI %q in output %q", tt.msg, tt.wantANSI, buf.String())
			}
		})
	}
}

func TestHandlerEntryAcceptedIsBold(t *testing.T) {
	var buf bytes.Buffer
	h := colorHandler(&buf, slog.LevelInfo)
	slog.New(h).Info("entry accepted")

	if !strings.Contains(buf.String(), ansiBold) {
		t.Errorf("entry accepted: expected bold in output: %q", buf.String())
	}
}

func TestHandlerEntryRejectedIsBold(t *testing.T) {
	var buf bytes.Buffer
	h := colorHandler(&buf, slog.LevelInfo)
	slog.New(h).Info("entry rejected")

	if !strings.Contains(buf.String(), ansiBold) {
		t.Errorf("entry rejected: expected bold in output: %q", buf.String())
	}
}

// --- WithAttrs ---

func TestHandlerWithAttrsFollowsMessage(t *testing.T) {
	var buf bytes.Buffer
	h := New(&buf, nil)

	slog.New(h).With("task", "foo").Info("entry accepted", "plugin", "seen")

	out := buf.String()
	taskIdx := strings.Index(out, "task=foo")
	msgIdx := strings.Index(out, "entry accepted")
	if taskIdx < 0 || msgIdx < 0 {
		t.Fatalf("missing content in output: %q", out)
	}
	if taskIdx < msgIdx {
		t.Errorf("pre-formatted attr should appear after message: %q", out)
	}
	if !strings.Contains(out, "plugin=seen") {
		t.Errorf("record-time attr missing: %q", out)
	}
}

func TestHandlerWithAttrsChained(t *testing.T) {
	var buf bytes.Buffer
	h := New(&buf, nil)

	slog.New(h).With("a", "1").With("b", "2").Info("msg")

	out := buf.String()
	if !strings.Contains(out, "a=1") || !strings.Contains(out, "b=2") {
		t.Errorf("chained WithAttrs: %q", out)
	}
	if strings.Index(out, "a=1") > strings.Index(out, "b=2") {
		t.Errorf("attrs should appear in call order: %q", out)
	}
}

// --- WithGroup ---

func TestHandlerWithGroupPrefixesPreattrs(t *testing.T) {
	var buf bytes.Buffer
	h := New(&buf, nil)

	slog.New(h).WithGroup("meta").With("k", "v").Info("msg")

	if !strings.Contains(buf.String(), "meta.k=v") {
		t.Errorf("group-prefixed pre-attr missing: %q", buf.String())
	}
}

func TestHandlerWithGroupPrefixesRecordAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := New(&buf, nil)

	slog.New(h).WithGroup("meta").Info("msg", "x", "y")

	if !strings.Contains(buf.String(), "meta.x=y") {
		t.Errorf("group-prefixed record attr missing: %q", buf.String())
	}
}

func TestHandlerEmptyGroupIgnored(t *testing.T) {
	var buf bytes.Buffer
	h := New(&buf, nil)

	slog.New(h).WithGroup("").Info("msg", "k", "v")

	out := buf.String()
	if strings.Contains(out, ".k=v") {
		t.Errorf("empty group produced prefix: %q", out)
	}
	if !strings.Contains(out, "k=v") {
		t.Errorf("attr missing after empty group: %q", out)
	}
}

// --- value formatting ---

func TestHandlerValueQuoting(t *testing.T) {
	var buf bytes.Buffer
	h := New(&buf, nil)

	slog.New(h).Info("msg",
		"plain", "value",
		"spaced", "has space",
		"empty", "",
		"eq", "a=b",
	)

	out := buf.String()
	for raw, want := range map[string]string{
		"plain=value":    "plain=value",
		`spaced=`:        `spaced="has space"`,
		`empty=`:         `empty=""`,
		`eq=`:            `eq="a=b"`,
	} {
		_ = raw
		if !strings.Contains(out, want) {
			t.Errorf("want %q in output: %q", want, out)
		}
	}
}

// --- Multi ---

func TestMultiDispatchesAll(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	m := Multi(New(&buf1, nil), New(&buf2, nil))

	slog.New(m).Info("broadcast")

	for i, b := range []*bytes.Buffer{&buf1, &buf2} {
		if !strings.Contains(b.String(), "broadcast") {
			t.Errorf("handler %d: message missing: %q", i+1, b.String())
		}
	}
}

func TestMultiWithAttrs(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	m := Multi(New(&buf1, nil), New(&buf2, nil))

	slog.New(m).With("k", "v").Info("msg")

	for i, b := range []*bytes.Buffer{&buf1, &buf2} {
		if !strings.Contains(b.String(), "k=v") {
			t.Errorf("handler %d: WithAttrs not propagated: %q", i+1, b.String())
		}
	}
}

func TestMultiWithGroup(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	m := Multi(New(&buf1, nil), New(&buf2, nil))

	slog.New(m).WithGroup("g").Info("msg", "k", "v")

	for i, b := range []*bytes.Buffer{&buf1, &buf2} {
		if !strings.Contains(b.String(), "g.k=v") {
			t.Errorf("handler %d: WithGroup not propagated: %q", i+1, b.String())
		}
	}
}

func TestMultiEnabledAny(t *testing.T) {
	ctx := context.Background()

	noneEnabled := Multi(
		New(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}),
		New(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}),
	)
	if noneEnabled.Enabled(ctx, slog.LevelInfo) {
		t.Error("Multi.Enabled should be false when no handler is enabled")
	}

	oneEnabled := Multi(
		New(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}),
		New(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelInfo}),
	)
	if !oneEnabled.Enabled(ctx, slog.LevelInfo) {
		t.Error("Multi.Enabled should be true when at least one handler is enabled")
	}
}

func TestMultiOnlyHandlesEnabledHandlers(t *testing.T) {
	var errorBuf, infoBuf bytes.Buffer
	m := Multi(
		New(&errorBuf, &slog.HandlerOptions{Level: slog.LevelError}),
		New(&infoBuf, &slog.HandlerOptions{Level: slog.LevelInfo}),
	)

	slog.New(m).Info("info msg")

	if strings.Contains(errorBuf.String(), "info msg") {
		t.Error("INFO message should not reach the ERROR-level handler")
	}
	if !strings.Contains(infoBuf.String(), "info msg") {
		t.Error("INFO message should reach the INFO-level handler")
	}
}
