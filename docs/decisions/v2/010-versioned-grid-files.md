# ADR-010: Versioned grid files + `grid.current` pointer

**Status:** Accepted (2026-05-18; D31)

**Context:**

Validation-pass-3 finding #5: `init.md` originally wrote a single
`.ghyll/grid.yaml` (one file, overwritten on each amendment);
`amendment.md` wrote versioned files (`.ghyll/grid.v(N+1).yaml`).
Two layouts for the same artifact. ADR-008 fixed arrow identity to
include `grid-version` — so the grid version is a first-class
concept, and the on-disk layout should reflect that.

**Decision:**

On-disk grid layout:

- Each commit writes a new `.ghyll/grid.v<N>.yaml`. Immutable after
  write.
- `.ghyll/grid.current` is a small pointer file. One line:
  `v<N>` naming the active version.
- Atomic update sequence on amendment / init write:
  1. Write content to `.ghyll/grid.v(N+1).yaml.tmp`.
  2. `fsync()` the temp file.
  3. `fsync()` the containing directory.
  4. `rename(.tmp, grid.v(N+1).yaml)` (atomic).
  5. `fsync()` the directory.
  6. Write `.ghyll/grid.current.tmp` containing `v<N+1>`.
  7. `rename(grid.current.tmp, grid.current)` (atomic).
- Init's first write produces `grid.v1.yaml` and
  `grid.current = "v1"`.
- Retention policy is operator-decided at init (default: keep all;
  may declare max-age or max-count).

**Consequences:**

- **Rules in:** versioned audit trail of grid history (every
  amendment leaves a file); atomic update visibility (a reader
  observing `grid.current = v(N+1)` is guaranteed to see
  `grid.v(N+1).yaml` intact due to fsync ordering); pointer
  indirection allows atomic version-bump without rewriting the
  grid content.
- **Rules out:** single-file overwrite-on-amendment (loses
  history); content-hash-named files (operator can't easily
  identify "current" or browse history by version).
- **Recovery surfaces** (FM-13, FM-14, FM-15):
  - `grid.current` points at missing file → engine refuses to
    start; operator must restore or re-point.
  - `grid.current` corrupted → engine refuses with
    `grid-current-malformed`.
  - Multiple grid versions but `grid.current` absent → engine does
    NOT silently pick latest; operator must declare.
- **fsync ordering** is load-bearing (FM-12 + cold-pass finding
  #10). The OS rename-before-fsync pattern is a real-world hazard
  on ext4 / NFS / etc. The schema specifies the order; the
  implementation must follow it.
- **Disk growth:** versioned files accumulate. A long-running
  project may have hundreds of grid versions. Retention policy
  prunes older versions; default keep-all is operator-overrideable
  at init.

See `gates.md` §2; `specs/architecture/components/amendment.md` F-3;
`specs/features/amendment.feature` "Successful atomic write of
v(N+1) with fsync ordering"; `specs/failure-modes.md` FM-12, 13,
14, 15.
