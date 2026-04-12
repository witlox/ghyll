package memory

import (
	"math"
	"os"
	"strings"
)

// Embedder generates vector embeddings from text.
// Uses ONNX Runtime when available (requires CGO + shared lib).
// Invariant 17: unavailable model disables features, doesn't crash.
type Embedder struct {
	modelPath  string
	dimensions int
	embedFunc  func(string) ([]float32, error) // nil when model unavailable
	cleanup    func()
}

// NewEmbedder creates an embedder. Attempts to load the ONNX model.
// Falls back gracefully if the model file or ONNX Runtime is unavailable.
func NewEmbedder(modelPath string, dimensions int) (*Embedder, error) {
	e := &Embedder{
		modelPath:  modelPath,
		dimensions: dimensions,
	}

	if _, err := os.Stat(modelPath); err != nil {
		return e, nil // model not downloaded
	}

	// Try ONNX Runtime (platform-dependent, may not be available)
	if err := tryInitONNX(e); err != nil {
		return e, nil // graceful degradation
	}

	return e, nil
}

// Embed generates a vector embedding for the given text.
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

// Close releases ONNX Runtime resources.
func (e *Embedder) Close() {
	if e.cleanup != nil {
		e.cleanup()
	}
}

// tokenize converts text to BERT-style token IDs.
// Uses a hash-based vocabulary-free approximation.
func tokenize(text string, maxLen int) []int64 {
	words := strings.Fields(strings.ToLower(text))
	tokens := make([]int64, 0, len(words)+2)
	tokens = append(tokens, 101) // [CLS]

	for _, word := range words {
		if len(tokens) >= maxLen-1 {
			break
		}
		h := int64(0)
		for _, c := range word {
			h = h*31 + int64(c)
		}
		if h < 0 {
			h = -h
		}
		tokens = append(tokens, (h%29000)+1000)
	}

	tokens = append(tokens, 102) // [SEP]
	return tokens
}

// meanPool averages hidden states across the sequence dimension.
func meanPool(data []float32, seqLen, dims int) []float32 {
	result := make([]float32, dims)
	for i := 0; i < seqLen; i++ {
		for j := 0; j < dims; j++ {
			result[j] += data[i*dims+j]
		}
	}
	scale := float32(seqLen)
	for j := range result {
		result[j] /= scale
	}
	return result
}

// normalize applies L2 normalization in-place.
func normalize(v []float32) {
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return
	}
	scale := float32(1.0 / norm)
	for i := range v {
		v[i] *= scale
	}
}
