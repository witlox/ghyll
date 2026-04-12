package context

import "math"

// DriftResult reports the outcome of a drift measurement.
type DriftResult struct {
	Similarity     float64
	Threshold      float64
	Drifted        bool
	ComparedTo     string // checkpoint hash measured against (invariant 28)
	BackfillNeeded bool
}

// MeasureDrift computes cosine similarity between current context and a checkpoint.
// Invariant 28: measures against most recent checkpoint.
func MeasureDrift(currentEmbedding, checkpointEmbedding []float32, checkpointHash string, threshold float64) DriftResult {
	if len(currentEmbedding) == 0 || len(checkpointEmbedding) == 0 {
		return DriftResult{
			Similarity: 1.0,
			Threshold:  threshold,
			ComparedTo: checkpointHash,
		}
	}

	sim := cosineSimilarity(currentEmbedding, checkpointEmbedding)
	drifted := sim < threshold

	return DriftResult{
		Similarity:     sim,
		Threshold:      threshold,
		Drifted:        drifted,
		ComparedTo:     checkpointHash,
		BackfillNeeded: drifted,
	}
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
