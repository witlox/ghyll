package memory

import (
	"fmt"
	"math"
	"os"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// Embedder generates vector embeddings from text using an ONNX model.
// Uses ONNX Runtime when the shared library and model are available.
// Invariant 17: unavailable model disables features, doesn't crash.
type Embedder struct {
	modelPath  string
	dimensions int
	embedFunc  func(string) ([]float32, error) // nil when model unavailable
	cleanup    func()
}

// NewEmbedder creates an embedder. Attempts to load the ONNX Runtime and model.
// Falls back gracefully if either is unavailable.
func NewEmbedder(modelPath string, dimensions int) (*Embedder, error) {
	e := &Embedder{
		modelPath:  modelPath,
		dimensions: dimensions,
	}

	// Check if model file exists
	if _, err := os.Stat(modelPath); err != nil {
		return e, nil // model not downloaded — graceful degradation
	}

	// Try to initialize ONNX Runtime
	session, cleanup, err := initONNXSession(modelPath, dimensions)
	if err != nil {
		// ONNX Runtime not available — graceful degradation
		return e, nil
	}

	e.cleanup = cleanup
	e.embedFunc = func(text string) ([]float32, error) {
		return session.embed(text)
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

// Close releases ONNX Runtime resources.
func (e *Embedder) Close() {
	if e.cleanup != nil {
		e.cleanup()
	}
}

// onnxSession wraps an ONNX Runtime session for embedding inference.
type onnxSession struct {
	session    *ort.DynamicAdvancedSession
	dimensions int
	mu         sync.Mutex
}

var ortInitOnce sync.Once
var ortInitErr error

func initONNXSession(modelPath string, dimensions int) (*onnxSession, func(), error) {
	// Initialize ONNX Runtime (once globally)
	ortInitOnce.Do(func() {
		ortInitErr = ort.InitializeEnvironment()
	})
	if ortInitErr != nil {
		return nil, nil, fmt.Errorf("onnx runtime init: %w", ortInitErr)
	}

	// Create session
	session, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"last_hidden_state"},
		nil,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("onnx session create: %w", err)
	}

	s := &onnxSession{
		session:    session,
		dimensions: dimensions,
	}

	cleanup := func() {
		_ = session.Destroy()
	}

	return s, cleanup, nil
}

func (s *onnxSession) embed(text string) ([]float32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Simple whitespace tokenizer with [CLS] and [SEP] tokens
	// GTE-micro uses BERT-style tokenization: [CLS]=101, [SEP]=102, [PAD]=0
	tokens := tokenize(text, 512)

	seqLen := int64(len(tokens))
	shape := ort.NewShape(1, seqLen)

	// Create input tensors
	inputIDs, err := ort.NewTensor(shape, tokens)
	if err != nil {
		return nil, fmt.Errorf("create input_ids tensor: %w", err)
	}
	defer func() { _ = inputIDs.Destroy() }()

	attentionMask := make([]int64, seqLen)
	for i := range attentionMask {
		attentionMask[i] = 1
	}
	attMaskTensor, err := ort.NewTensor(shape, attentionMask)
	if err != nil {
		return nil, fmt.Errorf("create attention_mask tensor: %w", err)
	}
	defer func() { _ = attMaskTensor.Destroy() }()

	tokenTypeIDs := make([]int64, seqLen)
	ttTensor, err := ort.NewTensor(shape, tokenTypeIDs)
	if err != nil {
		return nil, fmt.Errorf("create token_type_ids tensor: %w", err)
	}
	defer func() { _ = ttTensor.Destroy() }()

	// Create output tensor
	outputShape := ort.NewShape(1, seqLen, int64(s.dimensions))
	output, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		return nil, fmt.Errorf("create output tensor: %w", err)
	}
	defer func() { _ = output.Destroy() }()

	// Run inference
	err = s.session.Run(
		[]ort.ArbitraryTensor{inputIDs, attMaskTensor, ttTensor},
		[]ort.ArbitraryTensor{output},
	)
	if err != nil {
		return nil, fmt.Errorf("onnx inference: %w", err)
	}

	// Mean pooling over the sequence dimension
	outputData := output.GetData()
	embedding := meanPool(outputData, int(seqLen), s.dimensions)

	// L2 normalize
	normalize(embedding)

	return embedding, nil
}

// tokenize converts text to BERT-style token IDs.
// Uses a simple character-hash approach as a vocabulary-free approximation.
// For production, replace with a proper WordPiece tokenizer.
func tokenize(text string, maxLen int) []int64 {
	words := strings.Fields(strings.ToLower(text))

	// [CLS] + tokens + [SEP]
	tokens := make([]int64, 0, len(words)+2)
	tokens = append(tokens, 101) // [CLS]

	for _, word := range words {
		if len(tokens) >= maxLen-1 {
			break
		}
		// Simple hash-based token ID (range 1000-30000 to avoid special tokens)
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
