# Validation — adversarial pass on implementation (pass 1)

Cold-context attack on the first slice of v2 Go code:
- `catalogue/` (loader + types + validator)
- `bootstrap/` (session, modify-rule, grid)

Mirrored the schema's own per-arrow adversarial phase: three
sub-activities (spec drift, open sweep, depth classification).

**Verdict.** Not ready to extend further. Three load-bearing defects
that will cascade into every downstream component, plus several
spec-drift items that violate the ADRs the code claims to implement.
Remediation pass required before more init / runner pieces land.

---

## Findings — hardest first

### High severity

1. **[bug, high]** `bootstrap/modify.go:25` `severityRank` omits
   `unevaluated`. `catalogue/validate.go:25` includes it in
   `canonicalSeverity`. A modify from `severity="medium"` →
   `"unevaluated"` returns "severity outside canonical enum" rather
   than `ErrModifyWeakening`. The two enums must agree.

2. **[bug, high]** `bootstrap/modify.go:82-91` numeric weakening uses
   `propF < origF`. NaN compares false to everything: setting
   `threshold = NaN` (or YAML `.nan`) bypasses raise-only silently.
   No NaN/Inf rejection. Negative-zero ok but signed-int overflow
   through float64 loses precision >2^53.

3. **[spec-drift, high]** ADR-010 step ordering in `grid.go:130-167`:
   sequence runs steps 1-5 + a post-rename `fsync(dir)` not in the
   ADR. Minor divergence; documentation drift.

4. **[bug, high]** `grid.go:117-180` Write is non-atomic across the
   grid+pointer pair. Crash after `rename(tmp→grid.v2.yaml)` but
   before pointer rename leaves disk with `grid.v2.yaml` present
   but `grid.current` still pointing to v1. Read returns v1; v2
   silently shadow-persists. FM-12 / FM-54 specify crash recovery
   for this — not implemented.

5. **[bug, high]** `grid.go:165` `os.Rename(gridTmp, gridPath)` will
   silently overwrite an existing target. ADR-010 says grid files
   are "immutable after write". No `O_EXCL` / exists-check.
   Operator running init twice silently overwrites `grid.v1.yaml`.

6. **[bug, high]** `catalogue/catalogue.go:91` checks `Contract == "machine"`
   after YAML unmarshal. A YAML with no `evaluator:` key or
   `evaluator: null` parses to zero-value EvaluatorContract (empty
   Contract string), then is rejected as `unsupported evaluator
   contract ""`. The error is technically correct but the diagnostic
   is misleading; missing field should say so.

### Medium severity

7. **[bug, medium]** `catalogue/catalogue.go:81` `yaml.Unmarshal`
   uses the default decoder. Duplicate keys in a YAML file
   (`concept: x` twice, `arguments:` twice) are silently
   last-wins. Should use yaml.v3 strict mode (KnownFields(true)
   on a Decoder).

8. **[security, medium]** `catalogue/catalogue.go:60-98` Load follows
   symlinks via `os.ReadFile`. Hostile
   `gates/concepts/compiles.yaml → /etc/passwd` reads `/etc/passwd`
   and produces a YAML parse-error containing the path. Also no
   max-file-size; a multi-GB YAML fills memory.

9. **[edge-case, medium]** `validate.go:139-146` `depth-tier`
   hardcodes `0..3`. ADR-011 fixes 4 tiers, but per-project labels
   are configurable. Magic numbers in validate.go with no link to
   `bootstrap.DefaultDepthLadder`.

10. **[bug, medium]** `session.go:65-72` order: trim → empty check,
    length on untrimmed bytes. `"  " + 255 chars + "  "` is 259
    bytes, fails ErrOpIDTooLong, even though the meaningful op-id is
    255. Spec says "non-empty after trim" — implies length also
    measured after trim.

11. **[security, medium]** `session.go:89-99` `isUnsafeOpIDRune`
    blocks only U+202E. Misses other bidi/invisible per CVE-2021-42574
    "trojan source": U+200E LRM, U+200F RLM, U+202A-U+202D, U+2066-U+2069
    bidi isolates, U+FEFF BOM, U+200B ZWSP. FM-51 said "unicode RTL
    override" but trojan-source defenses block the whole bidi range.

12. **[security, medium]** `session.go` no NFKC/NFC normalization.
    Operator `"alıce"` (Turkish dotless i) vs `"alice"` — different
    op-ids, look identical. Attestation records two identities for
    one human. FM-50 punted on spoofing but normalization is a
    partial defense the code doesn't attempt.

13. **[error-handling, medium]** `grid.go:222-226` `fsyncDir`
    `defer d.Close()` — Close error ignored. Pattern repeated.
    Last-line-of-defense durability check missed.

14. **[bug, medium]** `validate.go:166-175` `int-or-range` accepts
    `[min, max]` but does NOT check `min <= max`. `[3, 0]` validates.
    Also doesn't apply concept's own `range:` constraint to the
    range bounds.

15. **[shallow-test, medium]** `validate_test.go:190-212`
    TestValidate_CardinalityCheckIntOrRange — asserts no error but
    doesn't verify the normalized output. Same pattern in
    TestValidate_DefaultApplied (line 137): checks key presence,
    doesn't assert default-value content matches schema.

16. **[shallow-test, medium]** `validate_test.go:71`
    TestValidate_TypeMismatch covers ONE catalogue type. The 14+
    type names declared in `types.go` (`path-glob`, `artifact-ref`,
    `language-id`, `severity`, `depth-tier`, `role-id`,
    `bounded-context-id`, `pass-id`, `arrow-id`, `dependency-id`,
    `finding-status`, `enum-or-path`, `int-or-range`, `command`,
    `duration`) are not tested for rejection.

17. **[bug, medium]** `validate.go:177-181` `default` case in
    checkType silently accepts any value. `enum-or-path` is listed
    in string-group but has no path/enum constraint.

18. **[bug, medium]** `modify.go:116` "for other types, equality
    required" uses `equalAny`, which returns false for map/slice.
    Identical list args (`markers: ["TODO"]` → `markers: ["TODO"]`)
    return `ErrModifyUnsupportedType` because slice comparison
    falls through.

19. **[bug, medium]** `modify.go:60` iterates `proposed` only. If
    operator OMITS an originally-present required arg from
    `proposed` (mutation-score with no threshold), the check passes
    silently — but that's clause-deletion, not modify. No detection.

20. **[edge-case, medium]** `grid.go:280-297` ReadVersion compares
    `g.GridVersion != version`. YAML scalar ambiguity: `grid-version: "1"`
    (quoted string) decodes to zero into int field, fails comparison
    quietly with a confusing error.

### Lower severity

21. **[race, medium]** `catalogue.Catalogue.concepts` map is read by
    Get/List/Count/Validate without locking. Doc says "immutable
    after construction" but no `sync.Once` or memory-barrier
    guarantee. Go race detector would flag.

22. **[error-handling, medium]** `grid.go:144-148` write-then-close:
    if `f.Write` partial-succeeds, code calls `f.Close()` (error
    ignored) and `os.Remove`. Close diagnostic lost behind the wrap.

23. **[edge-case, low]** `grid.go:130` huge GridVersion formats but
    `ReadCurrent` strconv.Atoi accepts up to MaxInt64; then exists-check
    fails. Mostly cosmetic.

24. **[shallow-test, low]** `grid_test.go:184` TestGrid_Write_MultipleVersions
    manually sets `g2.GridVersion = 2`. No `Grid.NextVersion()` helper —
    amendment-component will hand-roll the increment.

25. **[spec-drift, low]** ADR-011 D20 requires init's adversarial
    phase to run when proposed grid is written. `grid.go` Write just
    persists; no hook for `no-open-finding` /
    `every-requirement-meets-min-depth` insertion. Acceptable for
    slice-1 but not noted as "not yet built".

26. **[shallow-test, low]** `catalogue_test.go:226`
    TestLoad_DuplicateConceptName is `t.Skip`. The duplicate-detection
    branch has zero coverage.

27. **[bug, low]** `grid.go:54-61` constants declared but inline
    string literals used in `grid.go:130` and `grid.go:281`. Drift
    if someone renames the constant.

28. **[edge-case, low]** `session.go:74` `strings.Contains(opID, "..")`
    blocks legitimate `bob..bob`-style. False positive intentional
    per test.

29. **[shallow-test, low]** `modify_test.go:173` TestCheckModification_NilCatalogue
    asserts only `err != nil`. Doesn't assert error wraps a sentinel.
    Same in TestCheckModification_UnknownConcept.

30. **[bug, info]** `validate.go:69` `if schema.Default != nil` —
    YAML `default: null` parses to nil; schema authors writing
    `default: null` get surprising behavior. Doc that "nil means no
    default" is correct but unhelpful.

---

## Remediation plan

**Block-and-fix (must remediate before extending):**

| # | Issue | Fix |
|---|---|---|
| 1 | severityRank missing `unevaluated` | Add `"unevaluated": 0`; document that it's the floor (any non-unevaluated severity is stricter) |
| 2 | NaN/Inf bypass | Reject NaN and ±Inf in numeric weakening; clear sentinel |
| 4 | Grid.Write non-atomic across grid+pointer | Add crash-recovery scan: if `grid.v<N+1>.yaml` exists but `grid.current` says v<N>, refuse to start until operator decides |
| 5 | Rename silently overwrites | Use O_CREATE\|O_EXCL on temp; check destination doesn't exist before rename |
| 6 | Missing evaluator-contract diagnostic | Distinguish missing-evaluator from non-machine-contract |
| 7 | YAML not strict | Use `yaml.Decoder.KnownFields(true)` + check decoder for duplicate-key warning |
| 10 | op-id length-check untrimmed | Measure length after trim |

**Security hardening (in the same commit):**

| # | Issue | Fix |
|---|---|---|
| 8 | Symlink-follow + unbounded read | `os.Lstat` check; max-size 256KB per schema file |
| 11 | Trojan-source Unicode | Block all bidi controls (U+200E, U+200F, U+202A-U+202E, U+2066-U+2069) + ZWSP/ZWNJ/BOM |
| 12 | Op-id UTF-8 normalization | Apply NFC normalization before comparison; store normalized form |

**Shallow-test uplift:**

| # | Issue | Fix |
|---|---|---|
| 15 | Cardinality-check normalized output untested | Assert returned `out` contains expected normalized values |
| 16 | Type-mismatch coverage only one type | Add table-driven test across all 14+ catalogue types |
| 29 | Nil-catalogue / unknown-concept error checks | Assert errors.Is on a documented sentinel |

**Deferred (acceptable for slice-1, fix when next slice references them):**

- #3 fsync ordering (cosmetic, ADR wording)
- #9 depth-tier hardcoded (document the magic; will revisit when project-init declares the ladder)
- #14 int-or-range bounds-order
- #17 default-case permissive
- #18 list/slice equality
- #19 deletion-via-omit detection
- #20 YAML scalar ambiguity
- #21 race-detector silence (doc the immutability claim with a comment)
- #22 deferred-close diagnostic loss
- #23-#28 misc low-severity polish
- #30 YAML null-default

These can be addressed when the relevant feature surfaces them; they
are not load-bearing for the current slice.
