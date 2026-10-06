package torrentpeek

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// These drive a real torrent client against a seeder in the same process, so
// the part that cannot be faked — AddTorrent, piece priorities, piece
// completion, reading bytes back — is exercised without a tracker or a swarm.
//
// Peers are added directly rather than announced, because what is under test
// is piece selection and read admission, not peer discovery.

const (
	swarmPieceLen = 64 << 10
	swarmPieces   = 8
)

// seedTestTorrent writes a file of known bytes, builds its metainfo and starts
// a client seeding it. It returns the metainfo and the seeder's port.
func seedTestTorrent(t *testing.T) (*metainfo.MetaInfo, int, []byte) {
	t.Helper()

	dir := t.TempDir()
	want := make([]byte, swarmPieceLen*swarmPieces)
	for i := range want {
		// A pattern that differs per piece, so a read served from the wrong
		// offset is caught rather than passing on zeros.
		want[i] = byte((i/swarmPieceLen)*7 + i%13)
	}
	path := filepath.Join(dir, "payload.bin")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}

	info := metainfo.Info{PieceLength: swarmPieceLen}
	if err := info.BuildFromFilePath(path); err != nil {
		t.Fatal(err)
	}

	mi := &metainfo.MetaInfo{}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi.InfoBytes = infoBytes

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dir
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.Seed = true
	cfg.ListenPort = 0
	seeder, err := torrent.NewClient(cfg)
	if err != nil {
		t.Skipf("cannot start a torrent client here: %v", err)
	}
	t.Cleanup(func() { _ = seeder.Close() })

	st, err := seeder.AddTorrent(mi)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyDataContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	port := seeder.LocalPort()
	if port == 0 {
		t.Skip("the seeder is not listening; the sandbox forbids it")
	}
	return mi, port, want
}

// leech adds the torrent to a fresh client pointed at the seeder.
func leech(t *testing.T, mi *metainfo.MetaInfo, port int) *torrent.Torrent {
	t.Helper()

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = t.TempDir()
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.NoUpload = true
	cfg.Seed = false
	cfg.ListenPort = 0
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		t.Skipf("cannot start a torrent client here: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })

	tt, err := cl.AddTorrent(mi)
	if err != nil {
		t.Fatal(err)
	}
	tt.AddPeers([]torrent.PeerInfo{{
		Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port},
	}})
	select {
	case <-tt.GotInfo():
	case <-time.After(10 * time.Second):
		t.Skip("metadata did not arrive")
	}
	return tt
}

// TestSwarmFetchesOnlyTheRequestedPieces is the claim the whole feature rests
// on: asking for the ends of a torrent downloads the ends, not the file.
func TestSwarmFetchesOnlyTheRequestedPieces(t *testing.T) {
	mi, port, want := seedTestTorrent(t)
	tt := leech(t, mi, port)

	pk, err := New(tt)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pk.Close() }()

	if got := pk.Size(); got != int64(len(want)) {
		t.Errorf("Size = %d, want the whole torrent's %d", got, len(want))
	}
	if got := pk.PieceLength(); got != swarmPieceLen {
		t.Errorf("PieceLength = %d, want %d", got, swarmPieceLen)
	}
	if got := pk.NumPieces(); got != swarmPieces {
		t.Fatalf("NumPieces = %d, want %d", got, swarmPieces)
	}
	if got := pk.Ends(); len(got) != 2 || got[0] != 0 || got[1] != swarmPieces-1 {
		t.Fatalf("Ends = %v, want the first and last piece", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := pk.Fetch(ctx, pk.Ends()...); err != nil {
		t.Fatalf("fetching the ends: %v", err)
	}

	// The requested pieces read back exactly.
	first := make([]byte, swarmPieceLen)
	if _, err := pk.ReadAt(first, 0); err != nil {
		t.Fatalf("reading the first piece: %v", err)
	}
	if !bytes.Equal(first, want[:swarmPieceLen]) {
		t.Error("the first piece read back wrong")
	}
	lastOff := int64(swarmPieceLen) * (swarmPieces - 1)
	last := make([]byte, swarmPieceLen)
	if _, err := pk.ReadAt(last, lastOff); err != nil {
		t.Fatalf("reading the last piece: %v", err)
	}
	if !bytes.Equal(last, want[lastOff:]) {
		t.Error("the last piece read back wrong")
	}

	// A middle piece was never asked for, so reading it must fail with the
	// piece named rather than block on the network or return zeros.
	var mp *MissingPieceError
	_, err = pk.ReadAt(make([]byte, 16), int64(swarmPieceLen)*3+8)
	if !errors.As(err, &mp) {
		t.Fatalf("reading an unfetched piece gave %v, want *MissingPieceError", err)
	}
	if mp.Piece != 3 {
		t.Errorf("named piece %d, want 3", mp.Piece)
	}

	// And the accounting reflects two pieces, which is what a private tracker
	// will have recorded.
	if got := pk.FetchedPieces(); got != 2 {
		t.Errorf("FetchedPieces = %d, want 2", got)
	}
	if got := pk.FetchedBytes(); got != 2*swarmPieceLen {
		t.Errorf("FetchedBytes = %d, want %d", got, 2*swarmPieceLen)
	}
}

// Fetching the piece a failed read named makes the read succeed: the retry
// loop's contract, end to end.
func TestSwarmFetchOnDemand(t *testing.T) {
	mi, port, want := seedTestTorrent(t)
	tt := leech(t, mi, port)
	pk, err := New(tt)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pk.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	off := int64(swarmPieceLen)*5 + 100
	_, err = pk.ReadAt(make([]byte, 32), off)
	var mp *MissingPieceError
	if !errors.As(err, &mp) {
		t.Fatalf("want a MissingPieceError first, got %v", err)
	}

	if err := pk.Fetch(ctx, pk.PieceOf(mp.Offset)); err != nil {
		t.Fatalf("fetching piece %d: %v", pk.PieceOf(mp.Offset), err)
	}
	b := make([]byte, 32)
	if _, err := pk.ReadAt(b, off); err != nil {
		t.Fatalf("after fetching the named piece: %v", err)
	}
	if !bytes.Equal(b, want[off:off+32]) {
		t.Error("the bytes read back wrong after the retry")
	}
}

// A Peeker satisfies io.ReaderAt, which is what the prober takes, and reports
// Size so a consumer looking for the image's real length sees it rather than
// what happens to be present.
func TestPeekerIsAReaderAtWithSize(t *testing.T) {
	mi, port, want := seedTestTorrent(t)
	pk, err := New(leech(t, mi, port))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pk.Close() }()

	var _ io.ReaderAt = pk
	var sized interface{ Size() int64 } = pk
	if sized.Size() != int64(len(want)) {
		t.Errorf("Size = %d, want %d", sized.Size(), len(want))
	}
}

func TestFetchRejectsPiecesOutsideTheTorrent(t *testing.T) {
	mi, port, _ := seedTestTorrent(t)
	pk, err := New(leech(t, mi, port))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pk.Close() }()

	if err := pk.Fetch(context.Background(), swarmPieces+5); err == nil {
		t.Error("a piece index past the end should be rejected")
	}
	if err := pk.Fetch(context.Background(), -1); err == nil {
		t.Error("a negative piece index should be rejected")
	}
}
