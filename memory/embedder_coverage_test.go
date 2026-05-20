package memory

import (
	"errors"
	"testing"
)

// Tier 3 coverage push — Embedder accessors + Close/IsAvailable
// graceful-degradation paths.

func TestScenario_Embedder_MissingModel_DegradesGracefully(t *testing.T) {
	e, err := NewEmbedder("/nonexistent/model.onnx", 384)
	if err != nil {
		t.Fatalf("NewEmbedder with missing model: %v; want nil (graceful)", err)
	}
	if e.IsAvailable() {
		t.Error("IsAvailable = true with missing model")
	}
	if got := e.Dimensions(); got != 384 {
		t.Errorf("Dimensions = %d; want 384", got)
	}
	_, err = e.Embed("hello")
	if !errors.Is(err, ErrEmbedderUnavail) {
		t.Errorf("Embed err = %v; want ErrEmbedderUnavail", err)
	}
	e.Close() // must not panic when cleanup is nil
}
