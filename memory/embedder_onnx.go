//go:build cgo

package memory

import (
	"fmt"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// tryInitONNX attempts to initialize the ONNX Runtime session.
// Only compiled when CGO is enabled.
func tryInitONNX(e *Embedder) error {
	session, cleanup, err := initONNXSession(e.modelPath, e.dimensions)
	if err != nil {
		return err
	}
	e.cleanup = cleanup
	e.embedFunc = func(text string) ([]float32, error) {
		return session.embed(text)
	}
	return nil
}

type onnxSession struct {
	session    *ort.DynamicAdvancedSession
	dimensions int
	mu         sync.Mutex
}

var ortInitOnce sync.Once
var ortInitErr error

func initONNXSession(modelPath string, dimensions int) (*onnxSession, func(), error) {
	ortInitOnce.Do(func() {
		ortInitErr = ort.InitializeEnvironment()
	})
	if ortInitErr != nil {
		return nil, nil, fmt.Errorf("onnx runtime init: %w", ortInitErr)
	}

	session, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"last_hidden_state"},
		nil,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("onnx session create: %w", err)
	}

	s := &onnxSession{session: session, dimensions: dimensions}
	cleanup := func() { _ = session.Destroy() }
	return s, cleanup, nil
}

func (s *onnxSession) embed(text string) ([]float32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tokens := tokenize(text, 512)
	seqLen := int64(len(tokens))
	shape := ort.NewShape(1, seqLen)

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

	outputShape := ort.NewShape(1, seqLen, int64(s.dimensions))
	output, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		return nil, fmt.Errorf("create output tensor: %w", err)
	}
	defer func() { _ = output.Destroy() }()

	err = s.session.Run(
		[]ort.ArbitraryTensor{inputIDs, attMaskTensor, ttTensor},
		[]ort.ArbitraryTensor{output},
	)
	if err != nil {
		return nil, fmt.Errorf("onnx inference: %w", err)
	}

	outputData := output.GetData()
	embedding := meanPool(outputData, int(seqLen), s.dimensions)
	normalize(embedding)
	return embedding, nil
}
