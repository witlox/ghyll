package memory

import (
	"testing"
)

// TestScenario_Embedder_Unavailable maps to:
// Scenario: Embedding model not available
// Invariant 17: graceful unavailability
func TestScenario_Embedder_Unavailable(t *testing.T) {
	embedder, err := NewEmbedder("/nonexistent/model.onnx", 384)
	if err != nil {
		t.Fatalf("NewEmbedder should not error (lazy load): %v", err)
	}

	_, err = embedder.Embed("test text")
	if err == nil {
		t.Fatal("expected error for unavailable model")
	}
	if err != ErrEmbedderUnavail {
		t.Errorf("expected ErrEmbedderUnavail, got: %v", err)
	}
}

// TestScenario_Embedder_Available tests with a mock/stub embedder
func TestScenario_Embedder_Available(t *testing.T) {
	embedder := &Embedder{
		dimensions: 384,
		embedFunc: func(text string) ([]float32, error) {
			// Stub: return a fixed embedding
			emb := make([]float32, 384)
			for i := range emb {
				emb[i] = float32(i) / 384.0
			}
			return emb, nil
		},
	}

	result, err := embedder.Embed("test text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 384 {
		t.Errorf("embedding dimensions = %d, want 384", len(result))
	}
}

// TestScenario_Embedder_IsAvailable
func TestScenario_Embedder_IsAvailable(t *testing.T) {
	// Unavailable
	e1, _ := NewEmbedder("/nonexistent/model.onnx", 384)
	if e1.IsAvailable() {
		t.Error("expected unavailable for nonexistent model")
	}

	// Available (with stub)
	e2 := &Embedder{
		dimensions: 384,
		embedFunc: func(text string) ([]float32, error) {
			return make([]float32, 384), nil
		},
	}
	if !e2.IsAvailable() {
		t.Error("expected available for stub embedder")
	}
}

// TestTokenize verifies BERT-style tokenization
func TestTokenize(t *testing.T) {
	tokens := tokenize("hello world", 512)

	// Should have [CLS] + tokens + [SEP]
	if tokens[0] != 101 {
		t.Errorf("first token should be [CLS]=101, got %d", tokens[0])
	}
	if tokens[len(tokens)-1] != 102 {
		t.Errorf("last token should be [SEP]=102, got %d", tokens[len(tokens)-1])
	}
	if len(tokens) != 4 { // [CLS] hello world [SEP]
		t.Errorf("expected 4 tokens, got %d", len(tokens))
	}

	// Deterministic
	tokens2 := tokenize("hello world", 512)
	for i := range tokens {
		if tokens[i] != tokens2[i] {
			t.Error("tokenization not deterministic")
			break
		}
	}
}

// TestTokenize_MaxLen verifies truncation
func TestTokenize_MaxLen(t *testing.T) {
	long := "word " // repeated
	text := ""
	for i := 0; i < 1000; i++ {
		text += long
	}
	tokens := tokenize(text, 128)
	if len(tokens) > 128 {
		t.Errorf("tokens length %d exceeds maxLen 128", len(tokens))
	}
}

// TestMeanPool verifies mean pooling
func TestMeanPool(t *testing.T) {
	// 2 sequence positions, 3 dimensions
	data := []float32{
		1.0, 2.0, 3.0, // pos 0
		3.0, 4.0, 5.0, // pos 1
	}
	result := meanPool(data, 2, 3)
	if len(result) != 3 {
		t.Fatalf("expected 3 dims, got %d", len(result))
	}
	// Mean of [1,3]=2, [2,4]=3, [3,5]=4
	if result[0] != 2.0 || result[1] != 3.0 || result[2] != 4.0 {
		t.Errorf("mean pool = %v, want [2 3 4]", result)
	}
}

// TestNormalize verifies L2 normalization
func TestNormalize(t *testing.T) {
	v := []float32{3.0, 4.0}
	normalize(v)
	// L2 norm of [3,4] = 5, so normalized = [0.6, 0.8]
	if v[0] < 0.59 || v[0] > 0.61 {
		t.Errorf("v[0] = %f, want ~0.6", v[0])
	}
	if v[1] < 0.79 || v[1] > 0.81 {
		t.Errorf("v[1] = %f, want ~0.8", v[1])
	}
}

// TestEmbedder_Close
func TestEmbedder_Close(t *testing.T) {
	e, _ := NewEmbedder("/nonexistent/model.onnx", 384)
	e.Close() // should not panic
}
