//go:build cgo

package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// bundledONNXLibrary returns the path to a libonnxruntime shared
// library shipped alongside the ghyll binary, or "" if none is
// present. CSCS and similar HPC hosts often lack sudo, so operators
// can't install onnxruntime-system-wide; bundling the lib inside
// the release tarball at <binary-dir>/lib/ lets ghyll find it
// without LD_LIBRARY_PATH gymnastics.
//
// Lookup order (first hit wins):
//   - <binary-dir>/lib/<platform-specific name>
//   - <binary-dir>/../lib/<platform-specific name>  (when ghyll is
//     under <prefix>/bin/ghyll and lib lives at <prefix>/lib/)
//
// Returns "" silently when nothing matches — the upstream init
// falls back to the system loader's default search path, which is
// the pre-bundling behavior.
func bundledONNXLibrary() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)

	// Per-OS canonical filename. The library team ships these
	// names on each platform; symlinks let us avoid hardcoding a
	// specific version.
	var names []string
	switch runtime.GOOS {
	case "linux":
		names = []string{"libonnxruntime.so", "libonnxruntime.so.1"}
	case "darwin":
		names = []string{"libonnxruntime.dylib", "libonnxruntime.1.dylib"}
	default:
		return ""
	}

	for _, root := range []string{dir, filepath.Join(dir, "..")} {
		for _, name := range names {
			candidate := filepath.Join(root, "lib", name)
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return ""
}

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
		// Prefer a bundled libonnxruntime when one ships with
		// the release tarball — operators on locked-down hosts
		// (CSCS, similar HPC) can't `sudo apt install`. When no
		// bundle exists, leave SetSharedLibraryPath untouched and
		// the system search path takes over (existing behavior).
		if path := bundledONNXLibrary(); path != "" {
			ort.SetSharedLibraryPath(path)
		}
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
