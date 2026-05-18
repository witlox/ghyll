Project status assessment. Run at the start of any session.

1. Read root `CLAUDE.md` — current phase, scope, conventions
2. Check source state: `ls cmd/*/*.go && go list ./...` — packages present?
3. Check test compile: `go build ./... 2>&1 | tail -10`
4. Check fidelity: `cat specs/fidelity/INDEX.md 2>/dev/null` — auditor run?
5. Check sweep state: `cat specs/fidelity/SWEEP.md 2>/dev/null` — in progress?
6. Check open escalations: `ls specs/escalations/*.md 2>/dev/null`
7. Check git status: uncommitted changes? Ahead/behind remote?

Report:
- Current phase and scope (from root CLAUDE.md)
- Packages implemented vs designed
- Test status (compiles? passes? coverage?)
- Fidelity status (sweep complete? confidence levels?)
- Open escalations
- Recommended next action
