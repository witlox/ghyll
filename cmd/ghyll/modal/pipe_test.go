package modal

import (
	"io"
	"os"
)

// pipe is a test helper that returns an os.Pipe so tests can
// simulate a blocking stdin read.
func pipe() (io.ReadCloser, io.WriteCloser, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	return r, w, nil
}
