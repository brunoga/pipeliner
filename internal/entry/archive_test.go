package entry

import "testing"

func TestArchivedPayload(t *testing.T) {
	for _, tc := range []struct {
		files []string
		want  bool
	}{
		{[]string{"Show.S01/show.s01.rar", "Show.S01/show.s01.r00", "Show.S01/show.s01.r01", "Show.S01/show.s01.nfo", "Show.S01/show.s01.sfv"}, true},
		{[]string{"Show.S01/show.s01.part01.rar", "Show.S01/show.s01.part02.rar"}, true},
		{[]string{"Show.S01.zip"}, true},
		{[]string{"Show.S01.7z.001", "Show.S01.7z.002"}, true},
		{[]string{"Show.S01.RAR"}, true},
		{[]string{"Show.S01/E01.mkv", "Show.S01/E02.mkv"}, false},
		{[]string{"Show.S01/E01.mkv", "Show.S01/Subs.rar"}, false},
		{[]string{"Show.S01/Sample/show.s01-sample.mkv", "Show.S01/show.rar", "Show.S01/show.r00"}, true},
		{[]string{"Show.S01/show.nfo"}, false},
		{nil, false},
	} {
		if got := ArchivedPayload(tc.files); got != tc.want {
			t.Errorf("ArchivedPayload(%v) = %v, want %v", tc.files, got, tc.want)
		}
	}
}

func TestSetTorrentInfoMarksAnArchivedPayload(t *testing.T) {
	e := New("x", "http://x")
	e.SetTorrentInfo(TorrentInfo{Files: []string{"a.rar", "a.r00"}})
	if !e.GetBool(FieldTorrentArchive) {
		t.Error("an archived payload should set torrent_archive")
	}
	e2 := New("y", "http://y")
	e2.SetTorrentInfo(TorrentInfo{Seeds: 3})
	if _, ok := e2.Fields[FieldTorrentArchive]; ok {
		t.Error("no file list, no torrent_archive")
	}
}
