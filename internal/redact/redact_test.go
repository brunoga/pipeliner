package redact

import "testing"

func TestString(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"jackett download link",
			`metainfo_torrent: Get "https://jackett.example/dl/torrenting/?jackett_apikey=abc123def&path=Q2ZESjhP&file=Star+Wars": 404`,
			`metainfo_torrent: Get "https://jackett.example/dl/torrenting/?jackett_apikey=REDACTED&path=Q2ZESjhP&file=Star+Wars": 404`},
		{"torznab search",
			`url=https://jackett.example/api/v2.0/indexers/all/results/torznab/api?apikey=abc123&t=search&q=x`,
			`url=https://jackett.example/api/v2.0/indexers/all/results/torznab/api?apikey=REDACTED&t=search&q=x`},
		{"parameter after others",
			`https://t.example/rss?cat=1&passkey=0123456789abcdef`,
			`https://t.example/rss?cat=1&passkey=REDACTED`},
		{"gazelle pair",
			`https://t.example/torrents.php?action=download&id=9&authkey=aaa&torrent_pass=bbb`,
			`https://t.example/torrents.php?action=download&id=9&authkey=REDACTED&torrent_pass=REDACTED`},
		{"case insensitive",
			`https://t.example/x?APIKEY=abc`,
			`https://t.example/x?APIKEY=REDACTED`},
		{"value ends at a quote",
			`entry="https://t.example/x?token=abc" next=1`,
			`entry="https://t.example/x?token=REDACTED" next=1`},
		{"percent-encoded inside another URL",
			`https://proxy.example/fetch?u=https%3A%2F%2Ft.example%2Fdl%3Fid%3D1%26apikey%3Dabc123`,
			`https://proxy.example/fetch?u=https%3A%2F%2Ft.example%2Fdl%3Fid%3D1%26apikey%3DREDACTED`},
		{"userinfo",
			`dial http://admin:hunter2@deluge.lan:8112/json failed`,
			`dial http://admin:REDACTED@deluge.lan:8112/json failed`},
		{"announce passkey suffix",
			`tracker http://t.example/announce/0123456789abcdef0123456789abcdef down`,
			`tracker http://t.example/announce/REDACTED down`},
		{"announce passkey prefix",
			`https://t.example:2710/0123456789abcdef0123456789abcdef/announce`,
			`https://t.example:2710/REDACTED/announce`},
		{"harmless parameters untouched",
			`https://t.example/search?q=monkey&t=movie&cat=2000`,
			`https://t.example/search?q=monkey&t=movie&cat=2000`},
		{"public announce untouched",
			`udp://tracker.opentrackr.org:1337/announce`,
			`udp://tracker.opentrackr.org:1337/announce`},
		{"log attribute named key untouched",
			`deleted entry key=Star.Wars.1977 bucket=movies`,
			`deleted entry key=Star.Wars.1977 bucket=movies`},
		{"plain text", `run finished accepted=3`, `run finished accepted=3`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := String(c.in); got != c.want {
				t.Errorf("String(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
			if got := string(Bytes([]byte(c.in))); got != c.want {
				t.Errorf("Bytes(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}
