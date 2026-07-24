package main

import "testing"

func TestRewriteTarget(t *testing.T) {
	const tag = "v1.13.0"
	base := "https://github.com/brunoga/pipeliner/blob/" + tag
	cases := []struct {
		name, target, dir, want string
	}{
		// Top README: configs/ is in-archive, plugins/ is not.
		{"top keeps configs link", "configs/README.md", ".", "configs/README.md"},
		{"top rewrites plugins link", "plugins/README.md", ".", base + "/plugins/README.md"},

		// configs/README: sibling .star stay relative, ../plugins escapes.
		{"configs keeps sibling star", "advanced-tv-pipeline.star", "configs", "advanced-tv-pipeline.star"},
		{"configs keeps ./star", "./news-fanout.star", "configs", "./news-fanout.star"},
		{"configs rewrites parent plugins", "../plugins/processor/modify/set/README.md", "configs",
			base + "/plugins/processor/modify/set/README.md"},

		// Left alone.
		{"external url", "https://flexget.com", ".", "https://flexget.com"},
		{"anchor only", "#installation", ".", "#installation"},
		{"protocol relative", "//example.com/x", ".", "//example.com/x"},

		// Anchor on an escaping link is preserved after the path.
		{"plugins link with anchor", "plugins/README.md#sinks", ".", base + "/plugins/README.md#sinks"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rewriteTarget(c.target, c.dir, tag); got != c.want {
				t.Errorf("rewriteTarget(%q, %q) = %q, want %q", c.target, c.dir, got, c.want)
			}
		})
	}
}

func TestRewriteLinksInline(t *testing.T) {
	const tag = "v1.13.0"
	in := "See [plugins/](plugins/README.md) and [configs/](configs/README.md) plus [FlexGet](https://flexget.com)."
	want := "See [plugins/](https://github.com/brunoga/pipeliner/blob/" + tag +
		"/plugins/README.md) and [configs/](configs/README.md) plus [FlexGet](https://flexget.com)."
	if got := rewriteLinks(in, ".", tag); got != want {
		t.Errorf("rewriteLinks:\n got: %q\nwant: %q", got, want)
	}
}

func TestInArchive(t *testing.T) {
	in := []string{"README.md", "configs/README.md", "configs/tv.star"}
	out := []string{"plugins/README.md", "docs/user-guide.html", "configs/sub/x.star", "configs/notes.txt"}
	for _, p := range in {
		if !inArchive(p) {
			t.Errorf("inArchive(%q) = false, want true", p)
		}
	}
	for _, p := range out {
		if inArchive(p) {
			t.Errorf("inArchive(%q) = true, want false", p)
		}
	}
}
