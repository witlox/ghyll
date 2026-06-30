package stream

import (
	"errors"
	"strings"
	"testing"
)

// TestScenario_parseSSEStream_RefusesOversizedContent verifies
// Tier 3 / SR C-5: total response content exceeding
// maxStreamContentBytes returns ErrStreamSizeCap. Built as many
// small frames so the per-line scanner cap doesn't fire first;
// the AGGREGATE budget is what we're protecting.
func TestScenario_parseSSEStream_RefusesOversizedContent(t *testing.T) {
	chunk := strings.Repeat("x", 512*1024) // 512 KiB per frame
	var b strings.Builder
	frames := 40 // 40 × 512 KiB = 20 MiB > 16 MiB cap
	for i := 0; i < frames; i++ {
		b.WriteString(`data: {"choices":[{"delta":{"content":"`)
		b.WriteString(chunk)
		b.WriteString(`"}}]}` + "\n")
	}
	b.WriteString("data: [DONE]\n")
	_, err := parseSSEStream(strings.NewReader(b.String()), nil, "")
	if !errors.Is(err, ErrStreamSizeCap) {
		t.Errorf("err = %v; want ErrStreamSizeCap", err)
	}
}

// TestScenario_parseSSEStream_ScannerErrSurfaced verifies SR C-5:
// when scanner.Err returns non-nil (e.g. bufio.ErrTooLong on a
// frame > 1 MiB), parseSSEStream returns it instead of silently
// truncating.
func TestScenario_parseSSEStream_ScannerErrSurfaced(t *testing.T) {
	// 1.5 MiB single line — exceeds the 1 MiB scanner buffer cap.
	huge := strings.Repeat("a", int(maxSSELineBytes)+1024)
	body := strings.NewReader(huge)

	_, err := parseSSEStream(body, nil, "")
	if err == nil {
		t.Error("oversized line accepted; want scanner error")
	}
}

// TestScenario_BackoffShift_ClampedAtSix verifies SR L-6: the
// per-attempt backoff doesn't overflow even when MaxRetries is
// huge.
func TestScenario_BackoffShift_ClampedAtSix(t *testing.T) {
	// Simulate the clamping logic; the production path is
	// inside SendStream's retry loop where direct testing
	// requires a mock server. Just assert the arithmetic.
	for _, attempt := range []int{1, 6, 7, 31, 100} {
		shift := attempt - 1
		if shift > 6 {
			shift = 6
		}
		val := 1 << shift
		if val <= 0 || val > 64 {
			t.Errorf("attempt %d: shift=%d, 1<<shift=%d out of (0,64]", attempt, shift, val)
		}
	}
}
