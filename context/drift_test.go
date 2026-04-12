package context

import (
	"testing"
)

// TestScenario_Drift_NoDrift maps to:
// Scenario: No drift detected
func TestScenario_Drift_NoDrift(t *testing.T) {
	result := MeasureDrift(
		[]float32{0.9, 0.1, 0.0},   // current context embedding
		[]float32{0.85, 0.15, 0.0}, // checkpoint embedding (similar)
		"cp-hash-2",
		0.7, // threshold
	)

	if result.Drifted {
		t.Errorf("expected no drift, similarity=%f", result.Similarity)
	}
	if result.BackfillNeeded {
		t.Error("backfill should not be needed")
	}
	if result.ComparedTo != "cp-hash-2" {
		t.Errorf("compared_to = %q", result.ComparedTo)
	}
}

// TestScenario_Drift_Detected maps to:
// Scenario: Drift detected against most recent checkpoint
func TestScenario_Drift_Detected(t *testing.T) {
	result := MeasureDrift(
		[]float32{0.9, 0.1, 0.0}, // current: about auth
		[]float32{0.0, 0.1, 0.9}, // checkpoint: about CSS (orthogonal)
		"cp-hash-3",
		0.7,
	)

	if !result.Drifted {
		t.Errorf("expected drift, similarity=%f", result.Similarity)
	}
	if !result.BackfillNeeded {
		t.Error("backfill should be needed")
	}
	if result.Similarity >= 0.7 {
		t.Errorf("similarity %f should be below threshold 0.7", result.Similarity)
	}
}

// TestScenario_Drift_ExactMatch
func TestScenario_Drift_ExactMatch(t *testing.T) {
	emb := []float32{0.5, 0.5, 0.5}
	result := MeasureDrift(emb, emb, "cp-hash", 0.7)

	if result.Drifted {
		t.Error("identical embeddings should not drift")
	}
	if result.Similarity < 0.99 {
		t.Errorf("similarity = %f, expected ~1.0", result.Similarity)
	}
}

// TestScenario_Drift_EmptyEmbeddings
func TestScenario_Drift_EmptyEmbeddings(t *testing.T) {
	result := MeasureDrift(nil, nil, "", 0.7)
	// Empty embeddings: can't measure, don't drift
	if result.Drifted {
		t.Error("empty embeddings should not trigger drift")
	}
}
