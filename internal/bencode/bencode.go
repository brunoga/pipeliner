// Package bencode implements a decoder for the BitTorrent bencode format.
//
// Bencode types:
//   - Integer:   i<decimal>e  → int64
//   - Byte string: <len>:<bytes> → []byte
//   - List:      l<items>e    → []any
//   - Dict:      d<pairs>e    → map[string]any  (keys are strings)
//
// Strings are returned as []byte to preserve binary fields (e.g. piece data).
// Callers that expect text can cast to string as needed.
package bencode

import (
	"crypto/sha1" //nolint:gosec // SHA1 is specified by the BitTorrent protocol (BEP 3) for info hashes; not a security choice.
	"fmt"
	"strconv"
	"strings"
)

// Decode decodes bencoded data and returns the top-level Go value.
func Decode(data []byte) (any, error) {
	d := &decoder{data: data}
	return d.decode()
}

// TorrentInfo holds the fields extracted from a .torrent file.
type TorrentInfo struct {
	Name         string
	InfoHash     string // hex-encoded SHA-1 of the raw bencoded info dict
	TotalSize    int64
	FileCount    int
	Files        []string // relative file paths within the torrent
	Announce     string
	AnnounceList []string
	Comment      string
	CreatedBy    string
	CreationDate int64
	IsPrivate    bool
}

// DecodeTorrent decodes a .torrent file and returns structured metadata.
func DecodeTorrent(data []byte) (*TorrentInfo, error) {
	d := &decoder{data: data}
	root, err := d.decodeDict()
	if err != nil {
		return nil, fmt.Errorf("bencode: torrent: %w", err)
	}
	if !d.infoSet {
		return nil, fmt.Errorf("bencode: torrent: missing info dict")
	}

	ti := &TorrentInfo{
		InfoHash: fmt.Sprintf("%x", d.infoSHA1),
	}

	if v, ok := root["announce"].([]byte); ok {
		ti.Announce = string(v)
	}
	if v, ok := root["announce-list"].([]any); ok {
		seen := map[string]bool{}
		for _, tier := range v {
			if t, ok := tier.([]any); ok {
				for _, u := range t {
					if s, ok := u.([]byte); ok {
						url := string(s)
						if !seen[url] {
							ti.AnnounceList = append(ti.AnnounceList, url)
							seen[url] = true
						}
					}
				}
			}
		}
	}
	if len(ti.AnnounceList) == 0 && ti.Announce != "" {
		ti.AnnounceList = []string{ti.Announce}
	}
	if v, ok := root["comment"].([]byte); ok {
		ti.Comment = string(v)
	}
	if v, ok := root["comment.utf-8"].([]byte); ok {
		ti.Comment = string(v) // prefer UTF-8 variant
	}
	if v, ok := root["created by"].([]byte); ok {
		ti.CreatedBy = string(v)
	}
	if v, ok := root["creation date"].(int64); ok {
		ti.CreationDate = v
	}

	info, ok := root["info"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("bencode: torrent: info is not a dict")
	}

	if v, ok := info["name.utf-8"].([]byte); ok {
		ti.Name = string(v)
	} else if v, ok := info["name"].([]byte); ok {
		ti.Name = string(v)
	}

	if v, ok := info["private"].(int64); ok && v == 1 {
		ti.IsPrivate = true
	}

	// Single-file vs multi-file torrent
	if length, ok := info["length"].(int64); ok {
		ti.TotalSize = length
		ti.FileCount = 1
		ti.Files = []string{ti.Name}
	} else if files, ok := info["files"].([]any); ok {
		ti.FileCount = len(files)
		for _, f := range files {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			if l, ok := fm["length"].(int64); ok {
				ti.TotalSize += l
			}
			// Reconstruct the file path from the "path" list.
			if parts, ok := fm["path"].([]any); ok {
				segments := make([]string, 0, len(parts))
				for _, p := range parts {
					if s, ok := p.([]byte); ok {
						segments = append(segments, string(s))
					}
				}
				if len(segments) > 0 {
					ti.Files = append(ti.Files, strings.Join(segments, "/"))
				}
			}
		}
	}

	return ti, nil
}

// --- internal decoder ---

type decoder struct {
	data     []byte
	pos      int
	infoSHA1 [20]byte // SHA-1 of raw info dict bytes
	infoSet  bool
}

func (d *decoder) decode() (any, error) {
	if d.pos >= len(d.data) {
		return nil, fmt.Errorf("bencode: unexpected end of data")
	}
	switch {
	case d.data[d.pos] == 'i':
		return d.decodeInt()
	case d.data[d.pos] == 'l':
		return d.decodeList()
	case d.data[d.pos] == 'd':
		return d.decodeDict()
	case d.data[d.pos] >= '0' && d.data[d.pos] <= '9':
		return d.decodeBytes()
	default:
		return nil, fmt.Errorf("bencode: unknown type byte %q at pos %d", d.data[d.pos], d.pos)
	}
}

func (d *decoder) decodeInt() (int64, error) {
	d.pos++ // skip 'i'
	end := -1
	for i := d.pos; i < len(d.data); i++ {
		if d.data[i] == 'e' {
			end = i
			break
		}
	}
	if end < 0 {
		return 0, fmt.Errorf("bencode: unterminated integer")
	}
	n, err := strconv.ParseInt(string(d.data[d.pos:end]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bencode: invalid integer: %w", err)
	}
	d.pos = end + 1
	return n, nil
}

func (d *decoder) decodeBytes() ([]byte, error) {
	colon := -1
	for i := d.pos; i < len(d.data); i++ {
		if d.data[i] == ':' {
			colon = i
			break
		}
	}
	if colon < 0 {
		return nil, fmt.Errorf("bencode: missing ':' in string at pos %d", d.pos)
	}
	length, err := strconv.Atoi(string(d.data[d.pos:colon]))
	if err != nil {
		return nil, fmt.Errorf("bencode: invalid string length: %w", err)
	}
	start := colon + 1
	end := start + length
	if end > len(d.data) {
		return nil, fmt.Errorf("bencode: string extends past end of data")
	}
	b := make([]byte, length)
	copy(b, d.data[start:end])
	d.pos = end
	return b, nil
}

func (d *decoder) decodeList() ([]any, error) {
	d.pos++ // skip 'l'
	var items []any
	for d.pos < len(d.data) && d.data[d.pos] != 'e' {
		item, err := d.decode()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if d.pos >= len(d.data) {
		return nil, fmt.Errorf("bencode: unterminated list")
	}
	d.pos++ // skip 'e'
	return items, nil
}

func (d *decoder) decodeDict() (map[string]any, error) {
	d.pos++ // skip 'd'
	m := map[string]any{}
	for d.pos < len(d.data) && d.data[d.pos] != 'e' {
		keyBytes, err := d.decodeBytes()
		if err != nil {
			return nil, fmt.Errorf("bencode: dict key: %w", err)
		}
		key := string(keyBytes)

		valueStart := d.pos
		val, err := d.decode()
		if err != nil {
			return nil, fmt.Errorf("bencode: value for key %q: %w", key, err)
		}
		// Capture raw bytes of the info dict for info hash computation.
		if key == "info" && !d.infoSet {
			d.infoSHA1 = sha1.Sum(d.data[valueStart:d.pos]) //nolint:gosec
			d.infoSet = true
		}
		m[key] = val
	}
	if d.pos >= len(d.data) {
		return nil, fmt.Errorf("bencode: unterminated dict")
	}
	d.pos++ // skip 'e'
	return m, nil
}
