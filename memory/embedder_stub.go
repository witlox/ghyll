//go:build !cgo

package memory

// tryInitONNX is a no-op when CGO is disabled.
// ONNX Runtime requires CGO for the shared library bindings.
func tryInitONNX(e *Embedder) error {
	return ErrEmbedderUnavail
}
