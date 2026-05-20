package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// AttestationTreeWriter writes attestation records to a per-pass
// JSONL tree structure required by the operator-attestation spec:
//
//	attestations/v<N>/<context>/stratum-<S>/<role-pair>/<pass-id>.jsonl
//
// where:
//
//	<N>          = grid version
//	<context>    = bounded-context-id (literal "init" for init arrows)
//	<S>          = stratum (literal "init" for init arrows)
//	<role-pair>  = source-role + "__" + target-role for 2-role arrows,
//	               source + "__" + adversary + "__" + target for
//	               adversary-augmented 3-role chains (Tier 2 / gate-1 F-3),
//	               literal "init" for init arrows (gate-1 F-18)
//	<pass-id>    = pass identifier (required; gate-1 F-6 rejects empty)
//
// Tier 2 (ADR-016 Part B) promotes this writer to the
// AttestationStore primaryWriter — the inline-blocking audit
// surface. The flat .ghyll/attestations.jsonl steps down to an
// Observer-only fanout for aggregate tail.
//
// Concurrency: an internal mutex serializes file-handle opens
// + writes. Per-pass files are opened lazily on first
// attestation for that pass and held open until Close.
//
// File open flags: O_RDWR | O_CREATE | O_APPEND (gate-1 F-11) so
// TruncateTrailingPartial can ReadAt to find the last newline.
type AttestationTreeWriter struct {
	mu sync.Mutex

	root string

	// files caches the per-pass open file handles so we don't
	// re-open + re-stat on every Record event. Closed by the
	// writer's Close method.
	files map[string]*os.File

	// fileSync wraps the per-file fsync; abstracted for tests.
	fileSync func(f *os.File) error

	closed      bool
	writeErrors int
	lastErr     error
	bus         *OperatorBus
}

// NewAttestationTreeWriter constructs a writer rooted at the given
// directory. The directory is created (with intermediate dirs) on
// the first Record; an empty path returns an error.
func NewAttestationTreeWriter(root string) (*AttestationTreeWriter, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("attestation-tree: root path must be non-empty")
	}
	return &AttestationTreeWriter{
		root:     root,
		files:    make(map[string]*os.File),
		fileSync: func(f *os.File) error { return f.Sync() },
	}, nil
}

// WithBus wires an OperatorBus so audit-durability failures
// publish OpEventAttestationAuditDurabilityFailed. Returns the
// receiver for chaining (matches AttestationJSONLWriter's API).
func (w *AttestationTreeWriter) WithBus(bus *OperatorBus) *AttestationTreeWriter {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bus = bus
	return w
}

// PrimaryWriter returns a func suitable for
// AttestationStore.SetPrimaryWriter. Per ADR-016 Part B the tree
// writer becomes the inline-blocking audit surface; failure
// returns the error inline so AttestationStore.Record fails
// closed (in-memory unchanged, no fanout to other observers).
//
// The PrimaryWriter publishes ErrPathComponentTooLong via the
// bus on path-truncation overflow (gate-1 F-17); the write
// still proceeds with the hash-substituted segment so the
// verdict is not lost.
func (w *AttestationTreeWriter) PrimaryWriter() func(AttestationRecord) error {
	return func(rec AttestationRecord) error {
		path, truncated, err := EncodeAttestationPath(rec)
		if err != nil {
			return fmt.Errorf("tree-writer: encode path: %w", err)
		}
		if truncated && w.bus != nil {
			w.bus.Publish(OperatorEvent{
				Kind:    OpEventPathTruncated,
				ArrowID: rec.ArrowID,
				PassID:  rec.PassID,
				Detail:  "path components hash-substituted; rec.Reason annotated",
			})
		}
		absPath := filepath.Join(w.root, path)

		w.mu.Lock()
		defer w.mu.Unlock()
		if w.closed {
			return errors.New("tree-writer: closed")
		}
		f, ferr := w.openCached(absPath)
		if ferr != nil {
			w.recordTreeFailure(rec, fmt.Errorf("open %s: %w", absPath, ferr))
			return fmt.Errorf("open %s: %w", absPath, ferr)
		}
		line, jerr := jsonlMarshal(newJsonlRecord(rec))
		if jerr != nil {
			w.recordTreeFailure(rec, fmt.Errorf("marshal: %w", jerr))
			return fmt.Errorf("marshal: %w", jerr)
		}
		if _, werr := f.Write(line); werr != nil {
			w.recordTreeFailure(rec, fmt.Errorf("write %s: %w", absPath, werr))
			return fmt.Errorf("write %s: %w", absPath, werr)
		}
		if w.fileSync != nil {
			if serr := w.fileSync(f); serr != nil {
				w.recordTreeFailure(rec, fmt.Errorf("fsync %s: %w", absPath, serr))
				return fmt.Errorf("fsync %s: %w", absPath, serr)
			}
		}
		return nil
	}
}

// Observer returns an AttestationObserver that mirrors the
// PrimaryWriter's write path but cannot fail the Record call
// (the AttestationObserver contract has no error return).
// Used as a fallback when SetPrimaryWriter isn't wired (e.g.,
// legacy tests). Production session.openEngine uses
// PrimaryWriter, not Observer.
func (w *AttestationTreeWriter) Observer() AttestationObserver {
	return func(e AttestationEvent) {
		if e.Kind != AttestationEventRecord {
			return
		}
		_ = w.PrimaryWriter()(e.Record)
	}
}

// TruncateTrailingPartialAll walks every <root>/v*/<ctx>/stratum-*/<role-pair>/<pass-id>.jsonl
// and trims any trailing partial line. Called by
// session.openEngineWithOptions after LoadFromTree returns
// truncated=true (gate-1 F-11). Errors on individual files are
// counted (via writeErrors) but don't abort the walk.
func (w *AttestationTreeWriter) TruncateTrailingPartialAll(root string) error {
	abs := root
	if abs == "" {
		abs = w.root
	}
	return filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // no tree yet; nothing to truncate
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if err := truncateTrailingPartialFile(path); err != nil {
			w.mu.Lock()
			w.writeErrors++
			w.lastErr = err
			w.mu.Unlock()
		}
		return nil
	})
}

// truncateTrailingPartialFile scans backward for the last
// newline in path and truncates everything after it. Mirrors
// AttestationJSONLWriter.TruncateTrailingPartial's behavior but
// for a path-addressed file (no writer state needed).
//
// Gate-2 SEC-C-2: refuse to open if `path` is a symlink. An
// attacker who can drop a symlink under the attestations tree
// (shared CI workspace, malicious grid-author) could cause
// Ghyll to truncate arbitrary writable files (e.g. ~/.ssh/known_hosts)
// at session start. The Lstat-before-Open closes that path; the
// open itself uses O_NOFOLLOW where supported.
func truncateTrailingPartialFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("tree-writer: refuse symlink %s", path)
	}
	f, err := openNoFollowRDWR(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	size := stat.Size()
	if size == 0 {
		return nil
	}
	const chunk int64 = 64 * 1024
	for end := size; end > 0; {
		readFrom := end - chunk
		if readFrom < 0 {
			readFrom = 0
		}
		buf := make([]byte, end-readFrom)
		if _, err := f.ReadAt(buf, readFrom); err != nil {
			return err
		}
		for i := len(buf) - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				cutAt := readFrom + int64(i) + 1
				if cutAt == size {
					return nil
				}
				if err := f.Truncate(cutAt); err != nil {
					return err
				}
				return f.Sync()
			}
		}
		if readFrom == 0 {
			// No newline anywhere → whole file is a partial.
			if err := f.Truncate(0); err != nil {
				return err
			}
			return f.Sync()
		}
		end = readFrom
	}
	return nil
}

// openCached returns the cached file handle for path or opens
// (O_CREATE|O_RDWR|O_APPEND per gate-1 F-11) a fresh one.
// Caller holds w.mu.
func (w *AttestationTreeWriter) openCached(path string) (*os.File, error) {
	if f, ok := w.files[path]; ok {
		return f, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// O_RDWR (not O_WRONLY) so future truncate operations can
	// ReadAt to find the last newline. ADR-016 + gate-1 F-11.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	w.files[path] = f
	return f, nil
}

// recordTreeFailure is the tree writer's analog of
// AttestationJSONLWriter.recordFailure. Caller holds w.mu.
func (w *AttestationTreeWriter) recordTreeFailure(rec AttestationRecord, err error) {
	w.writeErrors++
	w.lastErr = err
	if w.bus != nil {
		w.bus.Publish(OperatorEvent{
			Kind:     OpEventAttestationAuditDurabilityFailed,
			ArrowID:  rec.ArrowID,
			ClauseID: rec.ClauseID,
			OpID:     rec.OpID,
			Detail:   "tree-writer: " + err.Error(),
		})
	}
}

// Close flushes and releases every cached file handle.
// Subsequent Observer events are silently dropped.
func (w *AttestationTreeWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	var firstErr error
	for _, f := range w.files {
		if w.fileSync != nil {
			_ = w.fileSync(f)
		}
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	w.files = map[string]*os.File{}
	return firstErr
}

// Root returns the tree root path.
func (w *AttestationTreeWriter) Root() string { return w.root }

// WriteErrors returns the count of marshal / open / write / fsync
// failures observed since open.
func (w *AttestationTreeWriter) WriteErrors() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeErrors
}

// LastError returns the most recent error, nil if none.
func (w *AttestationTreeWriter) LastError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr
}

// jsonlMarshal serializes one attestation record as the JSON
// payload + newline.
func jsonlMarshal(r jsonlRecord) ([]byte, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(data)+1)
	out = append(out, data...)
	out = append(out, '\n')
	return out, nil
}

// --- Path encoding ---------------------------------------------------

// maxPathComponentBytes is the per-segment byte limit on ext4 /
// btrfs / NTFS (255). Beyond this, EncodeAttestationPath
// substitutes a SHA-256-prefix hash and reports truncated=true.
const maxPathComponentBytes = 255

// EncodeAttestationPath computes the relative tree path for
// a record per ADR-016 Part F (gate-1-remediated). Pure
// function — no Grid argument (gate-1 F-2). Returns the
// tree-root-relative path, a `truncated` flag indicating
// whether any segment was hash-substituted (operator should
// see ErrPathComponentTooLong via the bus), and an error.
//
// Algorithm:
//
//	v<grid_version> / <context> / stratum-<stratum> / <role-pair> / <pass-id>.jsonl
//
// Init arrows (rec.AttestedByRole == "init"):
//
//	role-pair  = "init"
//	context    = "init"
//	stratum    = "init"
//
// 3-role chain (rec.AdversaryRole != ""):
//
//	role-pair  = "{source}__{adversary}__{target}"
//
// 2-role arrow:
//
//	role-pair  = "{source}__{target}"
//
// Empty PassID rejects with ErrAttestationPassIDEmpty (gate-1 F-6).
// Per-component overflow OR empty-after-sanitize triggers the
// hash fallback (h-<sha256[:16]>) and sets truncated=true.
func EncodeAttestationPath(rec AttestationRecord) (string, bool, error) {
	if strings.TrimSpace(rec.PassID) == "" {
		return "", false, ErrAttestationPassIDEmpty
	}

	isInit := strings.EqualFold(strings.TrimSpace(rec.AttestedByRole), "init")

	var (
		context  string
		stratum  string
		rolePair string
	)
	if isInit {
		// Per attestation.feature "init arrow path encoding":
		// role-pair is "init__<target>", context + stratum become
		// "_" placeholders (init is project-scoped, not context-
		// scoped — per components/init.md sub-phase A).
		context = "_"
		stratum = "_"
		target := strings.TrimSpace(rec.TargetRole)
		if target == "" {
			rolePair = "init"
		} else {
			rolePair = "init__" + target
		}
	} else {
		context = rec.Context
		stratum = rec.Stratum
		if rec.AdversaryRole != "" {
			rolePair = strings.Join([]string{
				rec.SourceRole, rec.AdversaryRole, rec.TargetRole,
			}, "__")
		} else {
			rolePair = rec.SourceRole + "__" + rec.TargetRole
		}
	}

	gv := fmt.Sprintf("v%d", rec.GridVersion)
	stratumSeg := "stratum-" + sanitizePathSegment(stratum)
	passFile := sanitizePathSegment(rec.PassID) + ".jsonl"

	gvSeg, t1 := safeSegment(gv)
	ctxSeg, t2 := safeSegment(sanitizePathSegment(context))
	stratumOut, t3 := safeSegment(stratumSeg)
	roleSeg, t4 := safeSegment(sanitizePathSegment(rolePair))
	passSeg, t5 := safeSegment(passFile)

	truncated := t1 || t2 || t3 || t4 || t5

	path := filepath.Join(gvSeg, ctxSeg, stratumOut, roleSeg, passSeg)
	return path, truncated, nil
}

// safeSegment enforces the per-component byte cap + zero-byte
// guard + path-traversal guard. Returns the segment (possibly
// hash-substituted) and a truncated flag.
//
//   - len(s) == 0 → returns "h-" + sha256[:16] of empty string.
//   - len(s) > maxPathComponentBytes → same hash substitution.
//   - s ∈ {".", ".."} → same hash substitution (gate-2 SEC-C-1:
//     `..` passes sanitizePathSegment's whitelist since `.` is
//     allowed; filepath.Join then normalizes the path and cancels
//     the parent. Substitute via hash so attacker-supplied Context
//     or Stratum cannot escape the v<N> subtree).
//   - Otherwise s passes through.
func safeSegment(s string) (string, bool) {
	if s == "" || len(s) > maxPathComponentBytes || s == "." || s == ".." {
		sum := sha256.Sum256([]byte(s))
		return "h-" + hex.EncodeToString(sum[:8]), true
	}
	return s, false
}

// sanitizePathSegment makes a string safe to use as a filesystem
// segment by replacing path separators and other directory-
// hostile characters with underscores. Conservative: anything
// that isn't [a-zA-Z0-9_.-] becomes "_".
func sanitizePathSegment(s string) string {
	if s == "" {
		return ""
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '_', c == '.', c == '-':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// File handle helpers — io.Writer compatibility for tests.
var _ io.Closer = (*AttestationTreeWriter)(nil)
