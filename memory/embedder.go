package memory

import "os"

// Embedder generates vector embeddings from text.
// Uses ONNX runtime when the model is available, degrades gracefully when not.
// Invariant 17: unavailable model disables features, doesn't crash.
type Embedder struct {
	modelPath  string
	dimensions int
	embedFunc  func(string) ([]float32, error) // nil when model unavailable
}

// NewEmbedder creates an embedder. Does not fail if model is missing (lazy).
func NewEmbedder(modelPath string, dimensions int) (*Embedder, error) {
	e := &Embedder{
		modelPath:  modelPath,
		dimensions: dimensions,
	}

	// Check if model file exists
	if _, err := os.Stat(modelPath); err == nil {
		// TODO: Initialize ONNX runtime session when onnxruntime_go is integrated.
		// For now, model file exists but we can't load it without the runtime.
		// This will be replaced with actual ONNX loading.
		e.embedFunc = nil
	}

	return e, nil
}

// Embed generates a vector embedding for the given text.
// Returns ErrEmbedderUnavail if the model is not loaded.
func (e *Embedder) Embed(text string) ([]float32, error) {
	if e.embedFunc == nil {
		return nil, ErrEmbedderUnavail
	}
	return e.embedFunc(text)
}

// IsAvailable reports whether the embedder can generate embeddings.
func (e *Embedder) IsAvailable() bool {
	return e.embedFunc != nil
}

// Dimensions returns the embedding vector dimensions.
func (e *Embedder) Dimensions() int {
	return e.dimensions
}
