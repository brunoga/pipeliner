package locale

import "testing"

func TestLanguageName639_1(t *testing.T) {
	cases := []struct{ code, want string }{
		{"en", "English"},
		{"fr", "French"},
		{"ja", "Japanese"},
		{"zh", "Chinese"},
		{"xx", "xx"}, // unknown code falls back to the raw code
		{"", ""},
	}
	for _, c := range cases {
		if got := LanguageName639_1(c.code); got != c.want {
			t.Errorf("LanguageName639_1(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestCountryName3166_1Alpha2(t *testing.T) {
	cases := []struct{ code, want string }{
		{"us", "United States"},
		{"gb", "United Kingdom"},
		{"jp", "Japan"},
		{"kr", "South Korea"},
		{"xx", "xx"},
		{"", ""},
	}
	for _, c := range cases {
		if got := CountryName3166_1Alpha2(c.code); got != c.want {
			t.Errorf("CountryName3166_1Alpha2(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}
