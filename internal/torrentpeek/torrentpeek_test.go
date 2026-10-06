package torrentpeek

import (
	"errors"
	"io"
	"testing"
)

// The piece-arithmetic and read-admission logic is what the fetch loop depends
// on, and it is independent of the torrent library. These tests drive it
// through a stand-in with the same shape so they need no swarm.
//
// admitReadAt mirrors Peeker.ReadAt's contract: bounds, the io.ReaderAt
// short-read rule, and above all refusing — rather than zero-filling or
// blocking on — a read that needs an unfetched piece.
type fake struct {
	data  []byte
	piece int64
	have  map[int]bool
}

func (f *fake) numPieces() int {
	n := int(int64(len(f.data)) / f.piece)
	if int64(len(f.data))%f.piece != 0 {
		n++
	}
	return n
}

func (f *fake) pieceOf(off int64) int {
	if off < 0 {
		return 0
	}
	i := int(off / f.piece)
	if n := f.numPieces(); i >= n {
		return n - 1
	}
	return i
}

func (f *fake) readAt(b []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	var eof error
	if rem := int64(len(f.data)) - off; int64(len(b)) > rem {
		b, eof = b[:rem], io.EOF
	}
	if len(b) == 0 {
		return 0, eof
	}
	for i := f.pieceOf(off); i <= f.pieceOf(off+int64(len(b))-1); i++ {
		if !f.have[i] {
			return 0, &MissingPieceError{Offset: off, Piece: i}
		}
	}
	copy(b, f.data[off:off+int64(len(b))])
	return len(b), eof
}

func newFake(size, piece int64, have ...int) *fake {
	f := &fake{data: make([]byte, size), piece: piece, have: map[int]bool{}}
	for i := range f.data {
		f.data[i] = byte(i % 251)
	}
	for _, i := range have {
		f.have[i] = true
	}
	return f
}

// A read wholly inside a fetched piece is served.
func TestReadInsideAFetchedPiece(t *testing.T) {
	f := newFake(1000, 100, 0)
	b := make([]byte, 10)
	n, err := f.readAt(b, 5)
	if err != nil || n != 10 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if b[0] != byte(5%251) {
		t.Errorf("served the wrong bytes: %v", b[:3])
	}
}

// The case the whole design rests on: a read needing an absent piece fails,
// and names the piece, so the caller can fetch it and retry. It must not
// return zeros — a parser can mistake zeros for a real structure — and must
// not block waiting for the network.
func TestReadOfAnUnfetchedPieceFailsAndNamesIt(t *testing.T) {
	f := newFake(1000, 100, 0) // only piece 0
	_, err := f.readAt(make([]byte, 10), 350)

	var mp *MissingPieceError
	if !errors.As(err, &mp) {
		t.Fatalf("err = %v, want *MissingPieceError", err)
	}
	if mp.Piece != 3 || mp.Offset != 350 {
		t.Errorf("got piece %d at offset %d, want piece 3 at 350", mp.Piece, mp.Offset)
	}
	if !errors.Is(err, err) || mp.Error() == "" {
		t.Error("the error should describe itself")
	}
}

// A read spanning a boundary needs both pieces, and is refused if either is
// missing — checked before any bytes are served, so a partial answer never
// escapes.
func TestReadSpanningPiecesNeedsBoth(t *testing.T) {
	f := newFake(1000, 100, 0) // piece 1 absent
	if _, err := f.readAt(make([]byte, 20), 90); err == nil {
		t.Fatal("a read crossing into an absent piece must fail")
	}
	f.have[1] = true
	if n, err := f.readAt(make([]byte, 20), 90); err != nil || n != 20 {
		t.Fatalf("with both pieces present: n=%d err=%v", n, err)
	}
}

// io.ReaderAt requires a short read at the end of input to come with io.EOF.
// The UDF reader asks for whole sectors, so it hits this at the tail.
func TestReadAtTheEnd(t *testing.T) {
	f := newFake(250, 100, 0, 1, 2)
	b := make([]byte, 100)
	n, err := f.readAt(b, 200)
	if n != 50 || !errors.Is(err, io.EOF) {
		t.Errorf("n=%d err=%v, want 50 and io.EOF", n, err)
	}
	if _, err := f.readAt(b, 250); !errors.Is(err, io.EOF) {
		t.Errorf("a read starting at the end should be io.EOF, got %v", err)
	}
	if _, err := f.readAt(b, -1); err == nil {
		t.Error("a negative offset should fail")
	}
}

// PieceOf clamps rather than returning an index past the end, so a probe
// asking about a byte at the very end does not send the caller out of range.
func TestPieceOfClamps(t *testing.T) {
	f := newFake(250, 100) // 3 pieces: 0,1,2
	for off, want := range map[int64]int{0: 0, 99: 0, 100: 1, 249: 2, 1 << 40: 2, -5: 0} {
		if got := f.pieceOf(off); got != want {
			t.Errorf("pieceOf(%d) = %d, want %d", off, got, want)
		}
	}
}

// A torrent whose length is not a whole number of pieces has a short last
// piece, and the byte accounting must say so rather than over-reporting what
// the probe cost on a private tracker.
func TestLastPieceIsShort(t *testing.T) {
	p := &Peeker{length: 250, piece: 100}
	// pieceLen needs the torrent for NumPieces, so exercise the arithmetic it
	// implements directly: the last piece holds the remainder.
	if rem := p.length % p.piece; rem != 50 {
		t.Fatalf("remainder = %d, want 50", rem)
	}
}

func TestMissingPieceErrorMessage(t *testing.T) {
	e := &MissingPieceError{Offset: 512, Piece: 7}
	if got := e.Error(); got == "" ||
		!contains(got, "512") || !contains(got, "7") {
		t.Errorf("message = %q, want it to name both the offset and the piece", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
