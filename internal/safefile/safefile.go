// Package safefile is the shared "limit + guard untrusted input"
// helpers Tier 3 / SR uses across memory, bootstrap, workflow,
// config, and vault_client. The patterns it bundles:
//
//   - SafeSegment(s): reject "..", "/", "\", NUL, ".", "" so a
//     join into filepath cannot escape the base dir
//   - ReadCappedFile(path, max): refuse a file > max bytes;
//     Lstat-then-open with O_NOFOLLOW so symlinks are denied
//   - ReadCappedReader(r, max): same cap for an io.Reader
//   - ValidateURLScheme(raw, allowed): refuse arbitrary URL
//     schemes outside the allowed set
//
// Pulled out of the various packages so the gate is uniform
// (Tier 3 security review distributed the same defenses across
// vault, attestation tree, etc.; centralizing them here keeps
// drift in check).
package safefile

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
)

// ErrSegmentInvalid is returned by SafeSegment when the segment
// contains a path-traversal character or is itself "." / "..".
var ErrSegmentInvalid = errors.New("safefile: segment contains path-traversal character")

// ErrFileTooLarge is returned by ReadCappedFile / ReadCappedReader
// when the input exceeds the configured byte cap.
var ErrFileTooLarge = errors.New("safefile: file exceeds size cap")

// ErrSymlinkRefused is returned by ReadCappedFile when the path
// is a symbolic link.
var ErrSymlinkRefused = errors.New("safefile: refusing symlink")

// ErrSchemeRejected is returned by ValidateURLScheme when the
// URL scheme is not in the allowlist.
var ErrSchemeRejected = errors.New("safefile: URL scheme not in allowlist")

// SafeSegment validates that s is safe to use as ONE filesystem
// segment joined into a base path. Returns ErrSegmentInvalid for:
//   - empty string
//   - "." or ".."
//   - any string containing "..", "/", "\\", or NUL
//
// Whitespace + control bytes are intentionally NOT rejected here
// (callers may want different policies); use this for the
// path-traversal class only.
func SafeSegment(s string) error {
	if s == "" || s == "." || s == ".." {
		return fmt.Errorf("%w: %q", ErrSegmentInvalid, s)
	}
	if strings.Contains(s, "..") ||
		strings.ContainsAny(s, "/\\\x00") {
		return fmt.Errorf("%w: %q", ErrSegmentInvalid, s)
	}
	return nil
}

// ReadCappedFile opens path (refusing symlinks via Lstat) and
// reads up to max bytes. Returns ErrFileTooLarge if the file is
// larger than max, ErrSymlinkRefused if the path is a symlink,
// or the OS error from Stat/Open.
func ReadCappedFile(path string, max int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %s", ErrSymlinkRefused, path)
	}
	if info.Size() > max {
		return nil, fmt.Errorf("%w: %d > %d (%s)", ErrFileTooLarge, info.Size(), max, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ReadCappedReader(f, max)
}

// ReadCappedReader reads up to max bytes from r. Returns
// ErrFileTooLarge if r yields more than max bytes (detected via
// a 1-byte probe past max).
func ReadCappedReader(r io.Reader, max int64) ([]byte, error) {
	limited := io.LimitReader(r, max+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: reader exceeded %d bytes", ErrFileTooLarge, max)
	}
	return data, nil
}

// ValidateURLScheme parses raw via net/url and returns nil iff
// the resulting Scheme is in allowed. Returns ErrSchemeRejected
// otherwise.
func ValidateURLScheme(raw string, allowed ...string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("safefile: parse url %q: %w", raw, err)
	}
	for _, ok := range allowed {
		if strings.EqualFold(u.Scheme, ok) {
			return nil
		}
	}
	return fmt.Errorf("%w: %q (allowed: %v)", ErrSchemeRejected, u.Scheme, allowed)
}
