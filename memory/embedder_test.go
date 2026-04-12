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
