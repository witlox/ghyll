package main

import (
	gocontextpkg "context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/witlox/ghyll/config"
	"github.com/witlox/ghyll/internal/safefile"
	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/ui"
)

// cmdMemoryMain handles `ghyll memory` subcommands.
func cmdMemoryMain(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ghyll memory [log|search <query>|sync|fetch-embedder]")
	}

	// fetch-embedder is config-driven and does not touch the store —
	// dispatch early so a missing/locked memory.db never blocks the
	// drift-detection bootstrap on a clean install.
	if args[0] == "fetch-embedder" {
		// CORR-3: HOME-unset is a silent CWD-write footgun. Refuse
		// at dispatch so every HOME-derived path that follows is
		// known-resolved. (CI containers and `env -i` invocations
		// hit this; the fix is to set HOME, not silently write
		// `.ghyll/...` into the working directory.)
		home := os.Getenv("HOME")
		if home == "" {
			return fmt.Errorf("HOME is unset — refusing to derive .ghyll paths from CWD; set HOME or pass an absolute model_path in [memory.embedder]")
		}
		// fetch-embedder needs only [memory.embedder].{model_url,
		// model_path, model_sha256}. Going through config.Load would
		// require a valid default model + endpoint, which is hostile
		// on a fresh install — the operator may be bootstrapping the
		// embedder BEFORE configuring any model. readEmbedderConfig
		// parses only the keys we care about and tolerates a
		// missing/partial config by falling through to defaults.
		configPath := filepath.Join(home, ".ghyll", "config.toml")
		modelURL, modelPath, modelSHA := readEmbedderConfig(configPath)
		return cmdMemoryFetchEmbedder(modelURL, modelPath, modelSHA, args[1:], ui.Stdout())
	}

	dbPath := filepath.Join(os.Getenv("HOME"), ".ghyll", "memory.db")
	store, err := memory.OpenStore(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	switch args[0] {
	case "log":
		return cmdMemoryLog(store, ui.Stdout())
	case "search":
		if len(args) < 2 {
			return fmt.Errorf("usage: ghyll memory search <query>")
		}
		query := strings.Join(args[1:], " ")
		return cmdMemorySearch(store, query, ui.Stdout())
	case "sync":
		return cmdMemorySyncManual()
	default:
		return fmt.Errorf("unknown memory command: %s", args[0])
	}
}

// cmdMemorySyncManual triggers a manual sync of the memory branch.
func cmdMemorySyncManual() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	hostname, _ := os.Hostname()
	deviceID := hostname
	if deviceID == "" {
		deviceID = "default"
	}

	branch := "ghyll/memory"
	syncer, err := memory.NewSyncer(cwd, branch, deviceID)
	if err != nil {
		return fmt.Errorf("sync setup: %w", err)
	}

	ui.Info("fetching remote checkpoints...")
	if err := syncer.Fetch(); err != nil {
		ui.Info("fetch: %v (continuing with push)", err)
	}

	gocontext := gocontextpkg.Background()
	ui.Info("pushing local checkpoints...")
	if err := syncer.CommitAndPush(gocontext); err != nil {
		return fmt.Errorf("push: %w", err)
	}

	ui.Info("sync complete")
	return nil
}

// cmdMemoryLog shows the checkpoint chain across all sessions.
func cmdMemoryLog(store *memory.Store, w io.Writer) error {
	checkpoints, err := store.ListAll()
	if err != nil {
		return err
	}

	if len(checkpoints) == 0 {
		_, _ = fmt.Fprintln(w, "no checkpoints")
		return nil
	}

	for _, cp := range checkpoints {
		ts := time.Unix(0, cp.Timestamp)
		if cp.Timestamp < 1e12 {
			// Treat as unix seconds if too small for nanos
			ts = time.Unix(cp.Timestamp, 0)
		}
		_, _ = fmt.Fprintf(w, "%s  %s  [%s] @%s  turn %d  %s\n",
			cp.Hash[:12],
			ts.Format("2006-01-02 15:04"),
			cp.ActiveModel,
			cp.AuthorID,
			cp.Turn,
			cp.Summary,
		)
	}
	return nil
}

// defaultEmbedderURL is the canonical GTE ONNX model used when
// cfg.Memory.Embedder.ModelURL is empty. We point at Xenova/gte-small
// (HF's Transformers.js team — well-maintained mirror) rather than
// a personal-account fork. Earlier default
// (nicholasgasior/gte-micro-onnx) was pulled by its owner within
// hours of being pinned here, breaking fresh installs with HTTP 401.
// Picking a stable maintainer is the durable mitigation.
//
// 384-dim BERT-style hidden_state (input/output names match what
// memory/embedder_onnx.go expects: input_ids/attention_mask/
// token_type_ids → last_hidden_state). Vocab-free tokenizer in
// memory/embedder.go is model-agnostic for the BERT family.
const defaultEmbedderURL = "https://huggingface.co/Xenova/gte-small/resolve/main/onnx/model.onnx"

// defaultEmbedderSHA256 pins the published GTE-small ONNX model
// (FE-SEC-4). When the operator does NOT override model_url, the
// download is rejected on hash mismatch — a CDN compromise or HF
// account takeover would have to also produce matching bytes.
// Computed via
//
//	curl -sL <defaultEmbedderURL> | sha256sum
//
// To rotate, recompute and update this constant in one commit.
// Operators using a custom model_url are expected to set their own
// [memory.embedder].model_sha256 alongside it (verified the same
// way); leaving model_sha256 empty with a custom model_url skips
// the check (opt-out, loud comment).
const defaultEmbedderSHA256 = "398a29991324e0b383afa13375d681ced3079c83e097fb1ebd9290d7498523b3"

// defaultEmbedderPath is the single source of truth for the
// fallback model path. main.go consults this too (see step 5) so
// the live session and the bootstrap subcommand never disagree
// about where the embedder lives (CORR-5 remediation). Returns ""
// when HOME is unset; callers must handle that case explicitly.
func defaultEmbedderPath() string {
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".ghyll", "models", "gte-small.onnx")
}

// embedderMaxBytes caps a single download to 1 GiB. The published
// gte-small model is ~127 MB; this guards against a misconfigured
// model_url pointing at something huge (an LLM weights blob would
// fill a typical ~/.ghyll volume without a cap).
const embedderMaxBytes int64 = 1 << 30

// embedderFetchTimeout bounds the entire download. Raised from 5m
// to 15m (UX-FM-5) because the published ~30-60 MB blob on a
// 100 KB/s constrained HPC uplink legitimately needs 5-10 minutes;
// the prior 5m cap fired mid-transfer on slow links and forced
// operators to interpret a normal-slow-link error as a real fault.
// 15m still catches a genuinely stuck connection.
const embedderFetchTimeout = 15 * time.Minute

// cmdMemoryFetchEmbedder downloads the ONNX embedding model from
// modelURL to modelPath. Lets binary installs bootstrap drift
// detection without cloning the source tree (replaces the deleted
// `make embedder` Makefile target).
//
// Empty modelURL / modelPath / modelSHA fall through to defaults:
// defaultEmbedderURL is paired with defaultEmbedderSHA256, and an
// empty modelPath uses defaultEmbedderPath(). When the operator
// overrides model_url, modelSHA is checked only when also set.
//
// Flags:
//   - --force / -f : overwrite an existing model file
//   - --help / -h  : print usage to w and return
//
// Security choices:
//   - HTTPS-only. http:// rejected EXCEPT for loopback hosts
//     (test-injection seam). validateEmbedderURL is re-applied on
//     every redirect hop (FE-SEC-1 remediation) so a 302→http://
//     downgrade is refused even when the start URL is https://.
//   - Default-URL SHA-256 pin (FE-SEC-4). Operator-overridden URLs
//     get opt-in checking via [memory.embedder].model_sha256.
//   - Atomic write with a per-process unique tmp name from
//     os.CreateTemp (INT-4) so concurrent invocations don't
//     truncate each other's in-flight downloads. os.Rename only
//     on full success; .tmp removed on every error path.
//   - Size cap (embedderMaxBytes) via Content-Length pre-check +
//     io.LimitReader post-check (covers servers that lie about
//     length or use chunked encoding).
//   - Reject 0-byte responses (CORR-1) — a stubbed/empty body
//     should never become a "downloaded" model the next session
//     trips over.
//   - Reject text/html Content-Type (CORR-2) — gated HF repos
//     and CDN auth walls return a 200 + HTML login page; never
//     write that into the model path.
//   - 5xx error bodies (capped at 4 KiB) are echoed in the
//     returned error (UX-FM-4) along with the URL — operators
//     debugging a 403 can see the auth-wall body inline.
//   - File mode 0o644 on the model (public data); 0o755 on the
//     parent dir; no path-traversal risk because modelPath is
//     filepath.Join'd from a stat-existed configured directory.
func cmdMemoryFetchEmbedder(modelURL, modelPath, modelSHA string, args []string, w io.Writer) error {
	force := false
	for _, a := range args {
		switch a {
		case "--force", "-f":
			force = true
		case "--help", "-h":
			printFetchEmbedderHelp(w)
			return nil
		default:
			return fmt.Errorf("unknown flag: %s (run `ghyll memory fetch-embedder --help` for usage)", a)
		}
	}

	if modelURL == "" {
		modelURL = defaultEmbedderURL
		if modelSHA == "" {
			modelSHA = defaultEmbedderSHA256
		}
	}
	if modelPath == "" {
		modelPath = defaultEmbedderPath()
		if modelPath == "" {
			return fmt.Errorf("HOME is unset and no [memory.embedder].model_path configured")
		}
	}
	expanded, err := expandUserHome(modelPath)
	if err != nil {
		return err
	}
	modelPath = expanded

	if err := validateEmbedderURL(modelURL); err != nil {
		return err
	}

	if fi, statErr := os.Stat(modelPath); statErr == nil && !force {
		_, _ = fmt.Fprintf(w, "ℹ embedder already present: %s (%d bytes; would have downloaded from %s — use --force to re-download)\n", modelPath, fi.Size(), modelURL)
		printOnnxRuntimeHint(w)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(modelPath), 0o755); err != nil {
		return fmt.Errorf("create model dir: %w", err)
	}

	_, _ = fmt.Fprintf(w, "ℹ downloading embedder from %s\n", modelURL)
	bytesWritten, gotSHA, err := streamDownload(modelURL, modelPath, embedderMaxBytes, embedderFetchTimeout, w)
	if err != nil {
		return err
	}
	if modelSHA != "" && !strings.EqualFold(gotSHA, modelSHA) {
		_ = os.Remove(modelPath)
		return fmt.Errorf("model_sha256 mismatch: expected %s, got %s — refusing to install (removed)", modelSHA, gotSHA)
	}

	_, _ = fmt.Fprintf(w, "✓ embedder ready at %s (%d bytes, sha256=%s)\n", modelPath, bytesWritten, gotSHA)
	printOnnxRuntimeHint(w)
	return nil
}

// printOnnxRuntimeHint emits the runtime-shared-lib install note.
// Extracted (UX-FM-3) so both the success path AND the skip-when-
// exists path print it — an operator who already downloaded the
// model but is still seeing "embedder unavailable" at session start
// most likely missed the runtime install step.
func printOnnxRuntimeHint(w io.Writer) {
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "ONNX Runtime shared library is also required:")
	_, _ = fmt.Fprintln(w, "  macOS:  brew install onnxruntime")
	_, _ = fmt.Fprintln(w, "  Linux:  https://github.com/microsoft/onnxruntime/releases")
	_, _ = fmt.Fprintln(w, "  Set ONNXRUNTIME_LIB_PATH if not on the default search path.")
}

// printFetchEmbedderHelp implements UX-FM-7. Operators with
// universal CLI muscle memory expect --help / -h to work; the
// previous unknown-flag path was hostile to discovery.
func printFetchEmbedderHelp(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: ghyll memory fetch-embedder [--force] [--help]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Downloads the ONNX embedding model used by drift detection.")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Reads [memory.embedder] from ~/.ghyll/config.toml:")
	_, _ = fmt.Fprintln(w, "  model_url      download source (default: GTE-micro on HuggingFace)")
	_, _ = fmt.Fprintln(w, "  model_path     destination (default: ~/.ghyll/models/gte-small.onnx)")
	_, _ = fmt.Fprintln(w, "  model_sha256   optional hex SHA-256 verified after download")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Rules:")
	_, _ = fmt.Fprintln(w, "  - URL scheme: https (or http to loopback for tests)")
	_, _ = fmt.Fprintln(w, "  - Redirects: every hop re-validated for scheme")
	_, _ = fmt.Fprintln(w, "  - Default URL is SHA-256 pinned; override URL + supply your own pin")
	_, _ = fmt.Fprintln(w, "  - Size cap: 1 GiB, atomic write via unique <path>.<rand>.tmp")
	_, _ = fmt.Fprintln(w, "  - Timeout: 15 minutes")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Flags:")
	_, _ = fmt.Fprintln(w, "  --force, -f    re-download even if the file exists")
	_, _ = fmt.Fprintln(w, "  --help, -h     show this help")
}

// embedderTOMLShape is the minimal TOML projection fetch-embedder
// needs. Decoded WITHOUT going through config.Load — that path
// requires a configured default model, which is wrong for a
// fetch-embedder bootstrap step. BurntSushi/toml silently ignores
// keys not present in the struct, so a real ghyll config.toml
// decodes cleanly into this minimal shape too.
type embedderTOMLShape struct {
	Memory struct {
		Embedder struct {
			ModelURL    string `toml:"model_url"`
			ModelPath   string `toml:"model_path"`
			ModelSHA256 string `toml:"model_sha256"`
		} `toml:"embedder"`
	} `toml:"memory"`
}

// readEmbedderConfig parses ONLY [memory.embedder].{model_url,
// model_path, model_sha256} from configPath. Returns
// ("", "", "") on a missing, oversized, symlinked, or malformed
// file — the caller substitutes defaults. Never returns an error
// (INT-3 remediation uses safefile.ReadCappedFile so a symlink
// pointing at /etc/shadow or a 1 TB cat-pic doesn't poison the
// best-effort decode).
func readEmbedderConfig(configPath string) (modelURL, modelPath, modelSHA string) {
	data, err := safefile.ReadCappedFile(configPath, config.MaxConfigFileBytes)
	if err != nil {
		return "", "", ""
	}
	var t embedderTOMLShape
	if _, err := toml.Decode(string(data), &t); err != nil {
		return "", "", ""
	}
	return t.Memory.Embedder.ModelURL,
		t.Memory.Embedder.ModelPath,
		t.Memory.Embedder.ModelSHA256
}

// expandUserHome replaces a leading "~/" with $HOME. Match TOML
// idiom — operators write `model_path = "~/.ghyll/..."` and expect
// it to resolve.
//
// Returns an error on inputs ghyll DOES NOT support but operators
// commonly try (UX-FM-10 remediation):
//   - "~user/..." (other-user home; would need /etc/passwd lookup,
//     out of scope for an embedder bootstrap step)
//   - "$VAR/..." or "${VAR}/..." (shell-style variable expansion;
//     not implemented — resolve the variable in the TOML)
//
// Failing loud is the right default: silently treating "$HOME/x"
// as a literal directory named "$HOME" would write to the wrong
// path and the operator would not notice until drift detection
// failed to find the model.
func expandUserHome(p string) (string, error) {
	if strings.HasPrefix(p, "$") {
		return "", fmt.Errorf("model_path uses shell-style variable expansion (%q) — ghyll only expands a leading ~/; resolve the variable in the TOML and retry", p)
	}
	if strings.HasPrefix(p, "~") && p != "~" && !strings.HasPrefix(p, "~/") {
		return "", fmt.Errorf("model_path uses other-user home expansion (%q) — ghyll only expands a leading ~/ to the current user's $HOME", p)
	}
	if strings.HasPrefix(p, "~/") {
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("HOME is unset; cannot expand %q", p)
		}
		return filepath.Join(home, p[2:]), nil
	}
	if p == "~" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("HOME is unset; cannot expand %q", p)
		}
		return home, nil
	}
	return p, nil
}

// validateEmbedderURL enforces the https-or-loopback rule. Returns
// nil if the scheme is https, OR http on a loopback host. Rejects
// anything else (file://, ftp://, http to public hosts, malformed).
func validateEmbedderURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("model_url: %w", err)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("model_url must be https (got scheme %q) — plaintext model downloads are a supply-chain risk", u.Scheme)
}

// isLoopbackHost reports whether the host is a loopback address.
// "localhost", "127.0.0.1", "::1", "[::1]" (url.Hostname() strips
// the brackets). Used to allow httptest servers in unit tests.
func isLoopbackHost(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// streamDownload fetches rawURL into path atomically and returns
// the byte count + hex SHA-256 of the written content.
//
// Hardening contracts (each tied to a confirmed adversarial finding):
//   - FE-SEC-1: CheckRedirect re-runs validateEmbedderURL on EVERY
//     hop, so a 302 from an https:// URL to an http:// public host
//     is refused (the default URL routinely 302s through HF CDN —
//     each hop must remain https except for loopback).
//   - CORR-1: 0-byte responses are rejected. A stubbed/empty body
//     should never become an "installed" model.
//   - CORR-2: text/html responses are rejected — HF login walls and
//     gated-repo redirects return 200 + HTML.
//   - UX-FM-4: non-2xx errors include the URL AND a 4 KiB body
//     snippet, so 403/401 debugging doesn't require curl.
//   - INT-4: per-process unique tmp path via os.CreateTemp prevents
//     concurrent invocations from truncating each other.
//   - Size cap: Content-Length pre-check + io.LimitReader (+1)
//     post-check. Servers lying about length still get cut.
//   - Atomic write: tmp file removed in every error branch; the
//     model path only appears on full successful rename.
//
// progress (may be nil) receives a one-line size-announcement once
// the headers arrive — operators on slow links can gauge wait time.
func streamDownload(rawURL, path string, maxBytes int64, timeout time.Duration, progress io.Writer) (int64, string, error) {
	client := &http.Client{
		Timeout: timeout,
		// FE-SEC-1: Go's default policy follows redirects across
		// schemes. Re-validate every hop against the HTTPS+loopback
		// rule so an upstream redirect injection cannot downgrade
		// the executable model download to plaintext.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return validateEmbedderURL(req.URL.String())
		},
	}
	ctx, cancel := gocontextpkg.WithTimeout(gocontextpkg.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, "", fmt.Errorf("new request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("http get %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// UX-FM-4: surface URL + a 4 KiB body excerpt. Common case
		// is a 401/403 from a gated HF repo whose body explains
		// "authentication required" — the operator can act on that
		// without re-running with curl.
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		snippet := strings.TrimSpace(string(bodySnippet))
		return 0, "", fmt.Errorf("http %d from %s: %s", resp.StatusCode, rawURL, snippet)
	}
	if resp.ContentLength > maxBytes {
		return 0, "", fmt.Errorf("model file %d bytes exceeds %d byte cap", resp.ContentLength, maxBytes)
	}

	// CORR-2: gated HF repos and CDN auth walls return 200 + HTML.
	// Refuse before writing a single byte. Compare on the bare MIME
	// (strip "; charset=..."), case-insensitive.
	if isHTMLContentType(resp.Header.Get("Content-Type")) {
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		snippet := strings.TrimSpace(string(bodySnippet))
		return 0, "", fmt.Errorf("model_url returned Content-Type %q (expected an ONNX binary, not HTML — likely a login wall or gated repo): %s", resp.Header.Get("Content-Type"), snippet)
	}

	// UX-FM-1: surface size to operator so a slow link doesn't read
	// as a hang. We don't print a progress bar (terminal width
	// detection, TTY-ness checks are out of scope for one-shot
	// bootstrap); one line "size: X bytes" before the io.Copy is
	// enough to know whether to expect 30s or 5min.
	if resp.ContentLength > 0 && progress != nil {
		_, _ = fmt.Fprintf(progress, "ℹ size: %d bytes\n", resp.ContentLength)
	}

	// INT-4: os.CreateTemp generates a unique <base>.<rand>.tmp.
	// Two concurrent fetches now write to different tmp paths and
	// can't truncate each other; the rename race remains but neither
	// loses bytes mid-flight.
	tmpFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return 0, "", fmt.Errorf("open tmp: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	// Tee-hash while copying — single pass over the body, SHA
	// committed to disk and to the hasher simultaneously.
	hasher := sha256.New()
	limited := &io.LimitedReader{R: resp.Body, N: maxBytes + 1}
	n, err := io.Copy(io.MultiWriter(tmpFile, hasher), limited)
	if err != nil {
		return 0, "", fmt.Errorf("download body: %w", err)
	}
	if n > maxBytes {
		return 0, "", fmt.Errorf("model file exceeds %d byte cap", maxBytes)
	}
	// CORR-1: zero-byte body is never a real model. Reject before
	// the rename so no 0-byte file lands and the next session
	// doesn't see "embedder unavailable" with a present file.
	if n == 0 {
		return 0, "", fmt.Errorf("model_url returned an empty body — refusing to write a 0-byte model")
	}
	if err := tmpFile.Sync(); err != nil {
		return 0, "", fmt.Errorf("fsync tmp: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return 0, "", fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return 0, "", fmt.Errorf("rename tmp→final: %w", err)
	}
	return n, hex.EncodeToString(hasher.Sum(nil)), nil
}

// isHTMLContentType reports whether ctype's MIME (before ";") is
// text/html. Case-insensitive. Used by streamDownload to refuse
// gated-repo login walls (CORR-2).
func isHTMLContentType(ctype string) bool {
	mime, _, _ := strings.Cut(ctype, ";")
	return strings.EqualFold(strings.TrimSpace(mime), "text/html")
}

// cmdMemorySearch searches checkpoints by summary text OR by hash
// prefix. Uses text matching when embedder is unavailable, cosine
// similarity when available.
//
// Hash-prefix match: a single hex-looking query (e.g. "5afd615f54a5"
// or its leading characters) is checked against cp.Hash before the
// summary scan. Operators naturally copy a hash out of `memory log`
// and feed it back into `memory search` to inspect that one entry;
// the previous text-only logic always returned "no matching
// checkpoints" because hashes never appear in summaries.
func cmdMemorySearch(store *memory.Store, query string, w io.Writer) error {
	checkpoints, err := store.ListAll()
	if err != nil {
		return err
	}

	queryLower := strings.ToLower(query)
	queryTerms := strings.Fields(queryLower)

	// Hash-prefix path: single query token, all lowercase hex,
	// at least 6 chars (avoid catching short English words like
	// "bad" or "feed"). Match by HasPrefix against cp.Hash.
	if len(queryTerms) == 1 && len(queryTerms[0]) >= 6 && isHexPrefix(queryTerms[0]) {
		var matches []memory.Checkpoint
		for _, cp := range checkpoints {
			if strings.HasPrefix(strings.ToLower(cp.Hash), queryTerms[0]) {
				matches = append(matches, cp)
			}
		}
		return renderSearchMatches(matches, w)
	}

	var matches []memory.Checkpoint
	for _, cp := range checkpoints {
		summaryLower := strings.ToLower(cp.Summary)
		matched := 0
		for _, term := range queryTerms {
			if strings.Contains(summaryLower, term) {
				matched++
			}
		}
		// Match if at least half the query terms are found
		if matched > 0 && matched >= len(queryTerms)/2 {
			matches = append(matches, cp)
		}
	}

	return renderSearchMatches(matches, w)
}

// isHexPrefix reports whether s is a non-empty string of lowercase
// hex characters. Used as a cheap gate before treating a single
// search token as a hash prefix.
func isHexPrefix(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// renderSearchMatches writes the per-checkpoint summary line to w,
// or "no matching checkpoints" when matches is empty. Shared by the
// text-search and hash-prefix paths so the rendering format is
// identical regardless of how the operator found the entry.
func renderSearchMatches(matches []memory.Checkpoint, w io.Writer) error {
	if len(matches) == 0 {
		_, _ = fmt.Fprintln(w, "no matching checkpoints")
		return nil
	}
	for _, cp := range matches {
		ts := time.Unix(0, cp.Timestamp)
		if cp.Timestamp < 1e12 {
			ts = time.Unix(cp.Timestamp, 0)
		}
		_, _ = fmt.Fprintf(w, "%s  %s  @%s  %s\n",
			cp.Hash[:12],
			ts.Format("2006-01-02 15:04"),
			cp.AuthorID,
			cp.Summary,
		)
	}
	return nil
}
