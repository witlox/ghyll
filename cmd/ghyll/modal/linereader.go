package modal

import (
	"bufio"
	"context"
	"io"
	"sync"
)

// LineReader is a session-scoped, single-goroutine line reader
// over an io.Reader. One reader goroutine consumes the underlying
// scanner; callers pull lines via Next(ctx).
//
// Gate-2 CONC-C-1/C-2: the previous TermModal constructed a fresh
// bufio.Scanner per PresentVerdict/PresentEscalation call. Two
// scanners over the same stdin lost buffered bytes when ctx
// cancelled mid-read AND leaked the reader goroutine (it kept
// blocking on Scan and consumed the next stdin byte on its own).
//
// LineReader fixes both: exactly one scanner + one goroutine per
// reader, and ctx-cancel just stops the caller's receive — the
// goroutine continues reading and buffers the next line for the
// next consumer. No bytes lost; no goroutines leaked.
type LineReader struct {
	lines     chan string
	closeOnce sync.Once
	done      chan struct{}
}

// defaultLineMaxBytes caps a single operator-typed line at 64
// KiB. Gate-2 SEC-M-1: the previous 1 MiB cap let the scanner
// buffer pre-validation residue notes much larger than the
// downstream ValidateUnitPayload cap (typically 16 KiB). Tighten
// at the reader so the rejection fires earlier (no megabyte
// allocations for an over-cap residue).
const defaultLineMaxBytes = 64 * 1024

// NewLineReader constructs and starts a LineReader over src.
// The reader goroutine runs until src returns EOF or Close() is
// called. Caller MUST call Close() before src is closed.
func NewLineReader(src io.Reader) *LineReader {
	r := &LineReader{
		lines: make(chan string, 16),
		done:  make(chan struct{}),
	}
	go r.readLoop(src)
	return r
}

func (r *LineReader) readLoop(src io.Reader) {
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 4096), defaultLineMaxBytes)
	for scanner.Scan() {
		select {
		case r.lines <- scanner.Text():
		case <-r.done:
			return
		}
	}
	// EOF or scanner.Err(): signal end-of-stream by closing the
	// channel. Callers see io.EOF from Next().
	close(r.lines)
}

// Next blocks until a line is available, ctx cancels, or the
// underlying reader EOFs. Returns the line + nil on success;
// "" + io.EOF on stream end; "" + ctx.Err() on cancel.
func (r *LineReader) Next(ctx context.Context) (string, error) {
	select {
	case line, ok := <-r.lines:
		if !ok {
			return "", io.EOF
		}
		return line, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Close stops the reader goroutine. Idempotent. Subsequent Next
// calls return the buffered lines first then io.EOF.
func (r *LineReader) Close() {
	r.closeOnce.Do(func() {
		close(r.done)
	})
}
