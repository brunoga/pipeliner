package exec

import (
	"reflect"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"plain", `convert a b`, []string{"convert", "a", "b"}},
		{"collapses runs of space", `convert   a    b`, []string{"convert", "a", "b"}},
		{"tabs and newlines separate too", "convert\ta\nb", []string{"convert", "a", "b"}},
		{"leading and trailing space", `  convert a  `, []string{"convert", "a"}},

		// The case the sink exists for: a media path.
		{"double-quoted path", `convert "/media/Life of Pi (2012)/x.mkv"`,
			[]string{"convert", "/media/Life of Pi (2012)/x.mkv"}},
		{"single-quoted path", `convert '/media/Life of Pi (2012)/x.mkv'`,
			[]string{"convert", "/media/Life of Pi (2012)/x.mkv"}},
		{"escaped spaces", `convert /media/Life\ of\ Pi/x.mkv`,
			[]string{"convert", "/media/Life of Pi/x.mkv"}},

		{"quotes may open mid-word", `a"b c"d`, []string{"ab cd"}},
		{"adjacent quoted sections", `"a""b"`, []string{"ab"}},
		{"empty quoted argument is kept", `cmd "" x`, []string{"cmd", "", "x"}},
		{"single quotes are literal", `cmd 'a\b$c"d'`, []string{"cmd", `a\b$c"d`}},
		{"escaped quote inside double quotes", `cmd "say \"hi\""`, []string{"cmd", `say "hi"`}},
		{"escaped backslash inside double quotes", `cmd "a\\b"`, []string{"cmd", `a\b`}},
		{"windows path keeps its backslashes when quoted", `cmd 'C:\Program Files\x.exe'`,
			[]string{"cmd", `C:\Program Files\x.exe`}},

		{"empty", ``, nil},
		{"only whitespace", `   `, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := splitArgs(c.in)
			if err != nil {
				t.Fatalf("splitArgs(%q): %v", c.in, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("splitArgs(%q) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
}

// Malformed quoting is an error rather than a silent best guess: the string is
// about to be executed, so a wrong split is worse than a refusal.
func TestSplitArgsRejectsMalformedQuoting(t *testing.T) {
	for _, in := range []string{`cmd "unterminated`, `cmd 'unterminated`, `cmd trailing\`} {
		if _, err := splitArgs(in); err == nil {
			t.Errorf("splitArgs(%q) should have failed", in)
		}
	}
}

// Nothing in the split gives a release title shell powers.
func TestSplitArgsIsNotAShell(t *testing.T) {
	got, err := splitArgs(`cmd "Movie; rm -rf /" "a|b" "$HOME" "*.mkv" "$(id)"`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cmd", "Movie; rm -rf /", "a|b", "$HOME", "*.mkv", "$(id)"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v — metacharacters must stay literal", got, want)
	}
}
