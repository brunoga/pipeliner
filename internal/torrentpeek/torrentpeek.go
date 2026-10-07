// Package torrentpeek reads a few pieces of a torrent without downloading it,
// so a release can be judged before it is fetched in full.
//
// A Blu-ray 3D image runs 20–50 GB and release names lie routinely about what
// is inside one. The container does not: a disc's own playlist says whether it
// carries an MVC dependent view, how long the feature runs and what streams it
// holds. That metadata sits in the image's first and last pieces, so 16–32 MiB
// of a 46 GB torrent settles questions no amount of title parsing can.
//
// The piece is the unit. BitTorrent cannot serve a smaller range, so a 32 KiB
// Matroska header still costs one piece — which is fine, because one piece of
// these torrents is under a thousandth of the file.
//
// # Reads of absent bytes are errors
//
// ReadAt refuses to serve a byte from a piece that has not been fetched, and
// the error names the offset. That is deliberate and it is what makes the
// fetch loop work: the prober reports the first offset it could not read, the
// caller turns that into a piece index, fetches it and retries. Returning
// zeros instead — the way a sparse file does — would leave the prober unable
// to say what it needs, and risks a parser accepting a zero-filled structure
// as real.
package torrentpeek

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
)

// MissingPieceError says a read touched a piece that has not been fetched.
// The caller is expected to fetch Piece and retry.
type MissingPieceError struct {
	Offset int64 // the read's offset into the torrent
	Piece  int   // the first unfetched piece the read needed
}

func (e *MissingPieceError) Error() string {
	return fmt.Sprintf("torrentpeek: offset %d needs piece %d, which has not been fetched", e.Offset, e.Piece)
}

// Peeker serves the pieces of one torrent that have been fetched, and refuses
// the rest. It satisfies io.ReaderAt, and reports Size so a consumer that
// probes for the length of the whole image (the UDF reader does) sees the real
// one rather than what happens to be present.
type Peeker struct {
	t      *torrent.Torrent
	length int64
	piece  int64

	// NoPeerTimeout gives up on a swarm that never answers, instead of
	// spending the caller's whole budget on it. Zero disables it.
	//
	// This is the common failure, not the rare one: a tracker scrape reports
	// seeders that may be long gone, so torrent_alive can pass a release that
	// no peer will serve a byte of. Measured on a live run, three such probes
	// burned the full two-minute budget each — six minutes of a six-minute
	// node — while a probe against a healthy swarm finished in 6.4 seconds.
	//
	// It fires only when there is nothing at all to wait for: no peer
	// connected AND no byte received. A slow-but-connected swarm keeps its
	// full budget, because that is the case where waiting pays off.
	NoPeerTimeout time.Duration

	mu      sync.Mutex
	r       torrent.Reader
	fetched map[int]bool // pieces this Peeker asked for, for accounting only
}

// New wraps a torrent whose info is already available.
func New(t *torrent.Torrent) (*Peeker, error) {
	info := t.Info()
	if info == nil {
		return nil, errors.New("torrentpeek: torrent metadata is not available yet")
	}
	if info.PieceLength <= 0 {
		return nil, fmt.Errorf("torrentpeek: implausible piece length %d", info.PieceLength)
	}
	r := t.NewReader()
	// Readahead would quietly prioritise pieces beyond the one being read,
	// which is the opposite of the point: the whole value here is fetching a
	// bounded, known set of pieces.
	r.SetReadahead(0)
	return &Peeker{
		t:       t,
		length:  t.Length(),
		piece:   info.PieceLength,
		r:       r,
		fetched: map[int]bool{},
	}, nil
}

// Size is the length of the whole torrent, not of what has been fetched.
func (p *Peeker) Size() int64 { return p.length }

// PieceLength is the smallest range BitTorrent can serve.
func (p *Peeker) PieceLength() int64 { return p.piece }

// NumPieces is how many pieces the torrent has.
func (p *Peeker) NumPieces() int { return p.t.NumPieces() }

// PieceOf returns the piece holding a byte offset, clamped to the torrent.
func (p *Peeker) PieceOf(off int64) int {
	if off < 0 {
		return 0
	}
	i := int(off / p.piece)
	if n := p.t.NumPieces(); i >= n {
		return n - 1
	}
	return i
}

// Ends returns the first and last piece indexes: where a disc image keeps its
// UDF directory and its BDMV metadata respectively.
func (p *Peeker) Ends() []int {
	last := p.t.NumPieces() - 1
	if last <= 0 {
		return []int{0}
	}
	return []int{0, last}
}

// Fetch requests the given pieces and waits for them, or for ctx to end.
// Pieces already present cost nothing. Duplicates are fine.
func (p *Peeker) Fetch(ctx context.Context, pieces ...int) error {
	var want []int
	for _, i := range pieces {
		if i < 0 || i >= p.t.NumPieces() {
			return fmt.Errorf("torrentpeek: piece %d is outside the torrent's %d", i, p.t.NumPieces())
		}
		if p.t.PieceBytesMissing(i) == 0 {
			continue
		}
		want = append(want, i)
	}
	if len(want) == 0 {
		return nil
	}
	for _, i := range want {
		p.mu.Lock()
		p.fetched[i] = true
		p.mu.Unlock()
		// DownloadPieces takes a half-open range, so one piece is [i, i+1).
		p.t.DownloadPieces(i, i+1)
	}

	// Polling rather than a completion channel: the library exposes per-piece
	// progress but no "these pieces are done" signal, and the wait is bounded
	// by ctx either way.
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	start := time.Now()
	missingAtStart := p.missing(want)
	for {
		missing := p.missing(want)
		if missing == 0 {
			return nil
		}
		if p.NoPeerTimeout > 0 && time.Since(start) > p.NoPeerTimeout &&
			missing == missingAtStart && len(p.t.PeerConns()) == 0 {
			return fmt.Errorf("torrentpeek: no peer answered for %s (%w)", p.NoPeerTimeout, ErrNoPeers)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("torrentpeek: fetching %d piece(s): %w", len(want), ctx.Err())
		case <-tick.C:
		}
	}
}

// ErrNoPeers reports that nothing in the swarm would serve the sample. It is
// distinct from a timeout because it says something a timeout does not: the
// full download would not have gone any better.
var ErrNoPeers = errors.New("no peer served any of the sample")

// missing totals the bytes still outstanding across the wanted pieces, which
// is what tells a stalled fetch from a slow one.
func (p *Peeker) missing(want []int) int64 {
	var n int64
	for _, i := range want {
		n += p.t.PieceBytesMissing(i)
	}
	return n
}

// FetchedBytes is how much this Peeker asked the network for: the accounting
// that matters on a private tracker, where probing is not free.
func (p *Peeker) FetchedBytes() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	var n int64
	for i := range p.fetched {
		n += p.pieceLen(i)
	}
	return n
}

// FetchedPieces is how many pieces this Peeker asked for.
func (p *Peeker) FetchedPieces() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.fetched)
}

func (p *Peeker) pieceLen(i int) int64 {
	if i == p.t.NumPieces()-1 {
		if rem := p.length % p.piece; rem != 0 {
			return rem
		}
	}
	return p.piece
}

// ReadAt implements io.ReaderAt over the fetched pieces. A read needing a
// piece that is absent fails with *MissingPieceError naming the offset, so the
// caller can fetch it and retry. Safe for concurrent use.
func (p *Peeker) ReadAt(b []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("torrentpeek: negative offset")
	}
	if off >= p.length {
		return 0, io.EOF
	}
	// Short the read at the end of the torrent, as io.ReaderAt requires.
	var eof error
	if rem := p.length - off; int64(len(b)) > rem {
		b, eof = b[:rem], io.EOF
	}
	if len(b) == 0 {
		return 0, eof
	}

	// Check every piece the read spans before reading any of it, so a read
	// that cannot be satisfied reports the piece it needs instead of blocking
	// on the torrent reader.
	first, last := p.PieceOf(off), p.PieceOf(off+int64(len(b))-1)
	for i := first; i <= last; i++ {
		if p.t.PieceBytesMissing(i) != 0 {
			return 0, &MissingPieceError{Offset: off, Piece: i}
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.r.Seek(off, io.SeekStart); err != nil {
		return 0, fmt.Errorf("torrentpeek: seeking to %d: %w", off, err)
	}
	// The pieces are present, so this must not wait on the network; a deadline
	// turns a library-level surprise into an error rather than a hung run.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p.r.SetContext(ctx)
	n, err := io.ReadFull(p.r, b)
	if err != nil {
		return n, fmt.Errorf("torrentpeek: reading %d bytes at %d: %w", len(b), off, err)
	}
	return n, eof
}

// Close releases the reader. It does not drop the torrent; the caller owns it.
func (p *Peeker) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.r.Close()
}
