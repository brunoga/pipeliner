package bencode

import (
	"fmt"
	"testing"
)

// --- primitive decoding ---

func TestDecodeInt(t *testing.T) {
	v, err := Decode([]byte("i42e"))
	if err != nil {
		t.Fatal(err)
	}
	if v.(int64) != 42 {
		t.Errorf("want 42, got %v", v)
	}
}

func TestDecodeNegativeInt(t *testing.T) {
	v, err := Decode([]byte("i-7e"))
	if err != nil {
		t.Fatal(err)
	}
	if v.(int64) != -7 {
		t.Errorf("want -7, got %v", v)
	}
}

func TestDecodeZeroInt(t *testing.T) {
	v, err := Decode([]byte("i0e"))
	if err != nil {
		t.Fatal(err)
	}
	if v.(int64) != 0 {
		t.Errorf("want 0, got %v", v)
	}
}

func TestDecodeString(t *testing.T) {
	v, err := Decode([]byte("5:hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(v.([]byte)) != "hello" {
		t.Errorf("want hello, got %v", v)
	}
}

func TestDecodeEmptyString(t *testing.T) {
	v, err := Decode([]byte("0:"))
	if err != nil {
		t.Fatal(err)
	}
	if string(v.([]byte)) != "" {
		t.Errorf("want empty, got %v", v)
	}
}

func TestDecodeList(t *testing.T) {
	v, err := Decode([]byte("l4:spam4:eggse"))
	if err != nil {
		t.Fatal(err)
	}
	list := v.([]any)
	if len(list) != 2 {
		t.Fatalf("want 2 items, got %d", len(list))
	}
	if string(list[0].([]byte)) != "spam" {
		t.Errorf("item 0: got %v", list[0])
	}
	if string(list[1].([]byte)) != "eggs" {
		t.Errorf("item 1: got %v", list[1])
	}
}

func TestDecodeEmptyList(t *testing.T) {
	v, err := Decode([]byte("le"))
	if err != nil {
		t.Fatal(err)
	}
	if list := v.([]any); len(list) != 0 {
		t.Errorf("want empty list, got %v", v)
	}
}

func TestDecodeDict(t *testing.T) {
	// bencode dicts have sorted keys
	v, err := Decode([]byte("d3:bar4:spam3:fooi42ee"))
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if string(m["bar"].([]byte)) != "spam" {
		t.Errorf("bar: got %v", m["bar"])
	}
	if m["foo"].(int64) != 42 {
		t.Errorf("foo: got %v", m["foo"])
	}
}

func TestDecodeEmptyDict(t *testing.T) {
	v, err := Decode([]byte("de"))
	if err != nil {
		t.Fatal(err)
	}
	if m := v.(map[string]any); len(m) != 0 {
		t.Errorf("want empty dict, got %v", v)
	}
}

func TestDecodeNestedList(t *testing.T) {
	v, err := Decode([]byte("ll1:ae1:be"))
	if err != nil {
		t.Fatal(err)
	}
	outer := v.([]any)
	if len(outer) != 2 {
		t.Fatalf("want 2, got %d", len(outer))
	}
	inner := outer[0].([]any)
	if string(inner[0].([]byte)) != "a" {
		t.Errorf("inner[0]: got %v", inner[0])
	}
}

// --- error cases ---

func TestDecodeUnterminatedInt(t *testing.T) {
	_, err := Decode([]byte("i42"))
	if err == nil {
		t.Error("expected error for unterminated int")
	}
}

func TestDecodeUnterminatedList(t *testing.T) {
	_, err := Decode([]byte("l4:spam"))
	if err == nil {
		t.Error("expected error for unterminated list")
	}
}

func TestDecodeUnterminatedDict(t *testing.T) {
	_, err := Decode([]byte("d3:foo"))
	if err == nil {
		t.Error("expected error for unterminated dict")
	}
}

func TestDecodeUnknownType(t *testing.T) {
	_, err := Decode([]byte("x"))
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestDecodeEmptyInput(t *testing.T) {
	_, err := Decode([]byte(""))
	if err == nil {
		t.Error("expected error for empty input")
	}
}

// --- torrent decoding ---

// bs encodes a string as a bencode byte string.
func bs(s string) string { return fmt.Sprintf("%d:%s", len(s), s) }

// bi encodes an integer as bencode.
func bi(n int64) string { return fmt.Sprintf("i%de", n) }

// minimalTorrent builds a minimal valid bencoded single-file torrent.
func minimalTorrent(name string, size int64) []byte {
	tracker := "http://tracker.example"
	pieces := string(make([]byte, 20)) // placeholder piece hash
	raw := "d" +
		bs("announce") + bs(tracker) +
		bs("comment") + bs("test torrent") +
		bs("created by") + bs("TestSuite") +
		bs("creation date") + bi(1700000000) +
		bs("info") + "d" +
		bs("length") + bi(size) +
		bs("name") + bs(name) +
		bs("piece length") + bi(262144) +
		bs("pieces") + bs(pieces) +
		"e" + // end info
		"e" // end root
	return []byte(raw)
}

func TestDecodeTorrentSingleFile(t *testing.T) {
	data := minimalTorrent("my.movie.mkv", 1_073_741_824)
	ti, err := DecodeTorrent(data)
	if err != nil {
		t.Fatal(err)
	}
	if ti.Name != "my.movie.mkv" {
		t.Errorf("name: got %q", ti.Name)
	}
	if ti.TotalSize != 1_073_741_824 {
		t.Errorf("size: got %d", ti.TotalSize)
	}
	if ti.FileCount != 1 {
		t.Errorf("file count: got %d", ti.FileCount)
	}
	if ti.Announce != "http://tracker.example" {
		t.Errorf("announce: got %q", ti.Announce)
	}
	if ti.Comment != "test torrent" {
		t.Errorf("comment: got %q", ti.Comment)
	}
	if ti.CreatedBy != "TestSuite" {
		t.Errorf("created by: got %q", ti.CreatedBy)
	}
	if ti.CreationDate != 1700000000 {
		t.Errorf("creation date: got %d", ti.CreationDate)
	}
	if len(ti.InfoHash) != 40 {
		t.Errorf("info hash: got %q (want 40 hex chars)", ti.InfoHash)
	}
}

func TestDecodeTorrentInfoHashStable(t *testing.T) {
	data := minimalTorrent("test", 1024)
	ti1, _ := DecodeTorrent(data)
	ti2, _ := DecodeTorrent(data)
	if ti1.InfoHash != ti2.InfoHash {
		t.Errorf("info hash not stable: %q vs %q", ti1.InfoHash, ti2.InfoHash)
	}
}

func TestDecodeTorrentMissingInfo(t *testing.T) {
	_, err := DecodeTorrent([]byte("d8:announce4:teste"))
	if err == nil {
		t.Error("expected error for missing info dict")
	}
}

func TestDecodeTorrentMultiFile(t *testing.T) {
	pieces := string(make([]byte, 20))
	raw := "d" +
		bs("announce") + bs("http://t.example") +
		bs("info") + "d" +
		bs("files") + "l" +
		"d" + bs("length") + bi(1000) + bs("path") + "l" + bs("a.txt") + "e" + "e" +
		"d" + bs("length") + bi(2000) + bs("path") + "l" + bs("b.txt") + "e" + "e" +
		"e" + // end files list
		bs("name") + bs("multi-torrent") +
		bs("piece length") + bi(262144) +
		bs("pieces") + bs(pieces) +
		"e" + // end info
		"e"
	ti, err := DecodeTorrent([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if ti.FileCount != 2 {
		t.Errorf("file count: want 2, got %d", ti.FileCount)
	}
	if ti.TotalSize != 3000 {
		t.Errorf("total size: want 3000, got %d", ti.TotalSize)
	}
	if ti.Name != "multi-torrent" {
		t.Errorf("name: got %q", ti.Name)
	}
}
