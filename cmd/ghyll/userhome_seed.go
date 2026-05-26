package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/witlox/ghyll/config"
)

// seedUserHome writes the embedded biased default user-home tree
// into homeDir (typically ~/.ghyll/) on first run. Seed-on-empty
// semantics: files that already exist are NEVER clobbered. The
// caller decides when to invoke (cmdRun calls it after ensureConfig
// so a fresh install gets the full bundle in one shot).
//
// Layout placed under homeDir (paths are relative):
//
//	instructions.md                # workflow router, auto-loads
//	commands/status.md
//	commands/verify.md
//	commands/spec-check.md
//	guidelines/engineering.md      # opt-in library — see ghyll init --language
//	guidelines/ci.md
//	guidelines/go.md
//	guidelines/python.md
//	guidelines/cpp.md
//	guidelines/rust.md
//
// Permissions: files 0o600 (consistent with config.toml; some
// commands document workflows operators consider personal),
// directories 0o700. Race-safe via O_EXCL — a concurrent seeder
// loses cleanly without clobbering.
//
// Returns the count of files actually written (0 on a re-run where
// every file already exists). errors only surface on filesystem
// failures other than EEXIST; EEXIST is the seed-on-empty success
// signal.
func seedUserHome(homeDir string) (written int, err error) {
	files := config.UserHomeFiles()
	paths := config.UserHomePathsSorted()
	for _, rel := range paths {
		dst := filepath.Join(homeDir, rel)
		if mkErr := os.MkdirAll(filepath.Dir(dst), 0o700); mkErr != nil {
			return written, fmt.Errorf("seed userhome dir %s: %w", filepath.Dir(dst), mkErr)
		}
		f, openErr := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if openErr != nil {
			if errors.Is(openErr, os.ErrExist) {
				// Operator already has this file; never clobber.
				continue
			}
			return written, fmt.Errorf("seed userhome file %s: %w", dst, openErr)
		}
		if _, wErr := f.Write(files[rel]); wErr != nil {
			_ = f.Close()
			return written, fmt.Errorf("write userhome file %s: %w", dst, wErr)
		}
		if cErr := f.Close(); cErr != nil {
			return written, fmt.Errorf("close userhome file %s: %w", dst, cErr)
		}
		written++
	}
	return written, nil
}
