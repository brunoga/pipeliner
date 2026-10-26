package bluray

import "testing"

func TestStripFormatTokensBundles(t *testing.T) {
	cases := map[string]string{
		"Eternals 4K + 3D Blu-ray":                                 "Eternals",
		"The Fantastic Four: First Steps 4K + 3D w/ STUFF Blu-ray": "The Fantastic Four: First Steps",
		"Avatar 3D Blu-ray":                                        "Avatar",
		"Avatar Blu-ray":                                           "Avatar",
	}
	for in, want := range cases {
		if got := StripFormatTokens(in); got != want {
			t.Errorf("StripFormatTokens(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCatalogKeyAgreesWithReleaseTitles(t *testing.T) {
	cases := [][2]string{
		{"Thunderbolts* 4K + 3D Blu-ray", "Thunderbolts"},
		{"The Fantastic Four: First Steps 4K + 3D Blu-ray", "The Fantastic 4: First Steps"},
		{"Star Wars: Episode IX - The Rise of Skywalker 3D Blu-ray", "Star Wars: The Rise of Skywalker"},
		{"Deadpool & Wolverine 4K + 3D Blu-ray", "Deadpool and Wolverine"},
	}
	for _, c := range cases {
		if a, b := CatalogKey(c[0]), CatalogKey(c[1]); a != b {
			t.Errorf("CatalogKey(%q) = %q, CatalogKey(%q) = %q; want equal", c[0], a, c[1], b)
		}
	}
	if CatalogKey("Fight Club") == CatalogKey("Fight Club 2") {
		t.Error("different films keyed alike")
	}
}
