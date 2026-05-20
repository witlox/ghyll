package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/witlox/ghyll/cmd/ghyll/modal"
	"github.com/witlox/ghyll/runner"
)

// modalDriver wires modal presentation to the operator bus +
// AttestationStore. Subscribes to OpEventAttestationRequested
// and OpEventInsufficientBasisRoundsExceeded; queues modal
// requests that the REPL turn loop drains via DrainPending.
//
// Tier 2 (ADR-016 Part D + gate-1 F-4/F-5/F-8/F-12/F-20).
type modalDriver struct {
	prompt        modal.OperatorModalPrompt
	store         *runner.AttestationStore
	passes        *runner.PassRegistry
	bus           *runner.OperatorBus
	ibTracker     *runner.InsufficientBasisTracker
	opIDProvider  func() string
	arrowResolver arrowResolverFn
	pendingMaxLen int

	mu       sync.Mutex
	pending  []modalRequest
	inFlight map[string]struct{} // gate-1 F-12: dedup on AttestationRecord.ID
}

// arrowResolverFn returns the per-arrow metadata needed to fill
// SourceRole/TargetRole/Context/Stratum/GridVersion on the
// AttestationRecord. The session-side wiring binds this to a
// closure over engine.Grid().Lookup. Returns (_, false) for
// arrows that aren't in the grid (e.g. orphan attestations after
// a grid swap); the driver records the verdict with empty roles
// — the §12.2 self-cert check is bypassed for that path, which
// matches the existing /attest CLI behavior.
type arrowResolverFn func(arrowID string) (arrowResolved, bool)

// arrowResolved is the resolved per-arrow metadata.
type arrowResolved struct {
	SourceRole  string
	TargetRole  string
	Context     string
	Stratum     string
	GridVersion uint64
}

// modalRequest is one queued presentation. The kind discriminates
// verdict-modal vs escalation-modal.
type modalRequest struct {
	kind        string // "verdict" | "escalation"
	hint        modal.Hint
	passID      string
	arrowID     string
	clauseID    string
	context     string
	stratum     string
	sourceRole  string
	targetRole  string
	attestRef   string
	gridVersion uint64
}

// ErrModalDrainCapExceeded is returned by DrainPending if the
// re-drain loop runs more than maxDrainRounds without emptying
// the pending queue. Indicates a runaway publisher; the operator
// surface gets a typed event.
var ErrModalDrainCapExceeded = errors.New("modal-drain-cap-exceeded")

// maxDrainRounds caps the number of snapshot-then-iterate rounds
// per DrainPending call (gate-1 F-5). 8 is enough for the
// common case (one arrow's clauses fan out into multiple
// requests; each verdict re-publishes the next clause's hint).
const maxDrainRounds = 8

// newModalDriver constructs + wires the driver. opIDProvider
// returns the active op-id AT CALL TIME (gate-1 F-20).
// pendingMaxLen is the cap on the queue (gate-1 F-8); overflow
// drops + emits OpEventModalBackpressure.
func newModalDriver(
	prompt modal.OperatorModalPrompt,
	store *runner.AttestationStore,
	passes *runner.PassRegistry,
	bus *runner.OperatorBus,
	ib *runner.InsufficientBasisTracker,
	opIDProvider func() string,
	arrowResolver arrowResolverFn,
	pendingMaxLen int,
) *modalDriver {
	if pendingMaxLen <= 0 {
		pendingMaxLen = 64
	}
	d := &modalDriver{
		prompt:        prompt,
		store:         store,
		passes:        passes,
		bus:           bus,
		ibTracker:     ib,
		opIDProvider:  opIDProvider,
		arrowResolver: arrowResolver,
		pendingMaxLen: pendingMaxLen,
		inFlight:      make(map[string]struct{}),
	}
	if bus != nil {
		bus.Subscribe(d.OnEvent)
	}
	return d
}

// OnEvent is the bus subscriber. Filters for the two Tier 2 event
// kinds + dedups on AttestationRecord.ID.
//
// Gate-1 F-8 backpressure: if the pending queue is at
// pendingMaxLen, drops the event and publishes
// OpEventModalBackpressure (the operator sees the loss).
func (d *modalDriver) OnEvent(ev runner.OperatorEvent) {
	switch ev.Kind {
	case runner.OpEventAttestationRequested:
		d.enqueueVerdict(ev)
	case runner.OpEventInsufficientBasisRoundsExceeded:
		d.enqueueEscalation(ev)
	}
}

func (d *modalDriver) enqueueVerdict(ev runner.OperatorEvent) {
	// Parse Detail as JSON Hint (Tier 2 Part G).
	var hint modal.Hint
	if ev.Detail != "" && ev.Detail[0] == '{' {
		_ = json.Unmarshal([]byte(ev.Detail), &hint)
	}
	if hint.ArrowID == "" {
		hint.ArrowID = ev.ArrowID
	}
	if hint.ClauseID == "" {
		hint.ClauseID = ev.ClauseID
	}
	d.appendRequest(modalRequest{
		kind:      "verdict",
		hint:      hint,
		passID:    ev.PassID,
		arrowID:   ev.ArrowID,
		clauseID:  ev.ClauseID,
		attestRef: hint.AttestationRef,
	})
}

func (d *modalDriver) enqueueEscalation(ev runner.OperatorEvent) {
	// Synthesize a deterministic attest-ref for the escalation
	// resolution. OpEventInsufficientBasisRoundsExceeded carries
	// no ref of its own (the IB stall is per-clause, not per-
	// attestation), but the resolution writes an
	// AttestationRecord which requires an ID. We derive it from
	// (arrow, clause, grid-version) so re-presentations of the
	// same escalation hit the dedup set.
	var gridVer uint64
	if d.arrowResolver != nil {
		if r, ok := d.arrowResolver(ev.ArrowID); ok {
			gridVer = r.GridVersion
		}
	}
	attRef := runner.ComputeAttestationID(
		runner.AttestationKindDepthType,
		ev.ArrowID, ev.ClauseID, gridVer,
	)
	d.appendRequest(modalRequest{
		kind: "escalation",
		hint: modal.Hint{
			ArrowID:        ev.ArrowID,
			ClauseID:       ev.ClauseID,
			AttestationRef: attRef,
		},
		passID:      ev.PassID,
		arrowID:     ev.ArrowID,
		clauseID:    ev.ClauseID,
		attestRef:   attRef,
		gridVersion: gridVer,
	})
}

func (d *modalDriver) appendRequest(req modalRequest) {
	d.mu.Lock()
	// Gate-1 F-8: cap enforcement.
	if len(d.pending) >= d.pendingMaxLen {
		d.mu.Unlock()
		if d.bus != nil {
			d.bus.Publish(runner.OperatorEvent{
				Kind:    runner.OpEventModalBackpressure,
				ArrowID: req.arrowID,
				PassID:  req.passID,
				Detail:  fmt.Sprintf("modal queue at cap %d; dropping event", d.pendingMaxLen),
			})
		}
		return
	}
	// Gate-1 F-12 dedup: skip if same attestation-ref already
	// in-flight or queued.
	if req.attestRef != "" {
		if _, ok := d.inFlight[req.attestRef]; ok {
			d.mu.Unlock()
			return
		}
		d.inFlight[req.attestRef] = struct{}{}
	}
	d.pending = append(d.pending, req)
	d.mu.Unlock()
}

// EnqueueFromRecovery folds Recovery's republish events into the
// pending queue (gate-1 F-4). Called from session.go:initEngine
// after attachJournal + bus.Subscribe have completed. Filters
// for OpEventRecoveryAttestationRepublished; constructs modal
// requests from the event's payload.
func (d *modalDriver) EnqueueFromRecovery(events []runner.OperatorEvent) {
	for _, ev := range events {
		if ev.Kind != runner.OpEventRecoveryAttestationRepublished {
			continue
		}
		// Extract the att-ref from Detail (recovery's payload format:
		// "att-ref=<ref> preserved at <stamp>").
		attRef := extractAttRef(ev.Detail)
		d.appendRequest(modalRequest{
			kind: "verdict",
			hint: modal.Hint{
				ArrowID:        ev.ArrowID,
				ClauseID:       ev.ClauseID,
				AttestationRef: attRef,
			},
			passID:    ev.PassID,
			arrowID:   ev.ArrowID,
			clauseID:  ev.ClauseID,
			attestRef: attRef,
		})
	}
}

// extractAttRef parses Recovery's Detail format "att-ref=<ref> ..."
// and returns the ref or "".
func extractAttRef(detail string) string {
	const prefix = "att-ref="
	idx := strings.Index(detail, prefix)
	if idx < 0 {
		return ""
	}
	rest := detail[idx+len(prefix):]
	end := strings.IndexAny(rest, " \t")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// DrainPending blocks until every queued modal request has been
// presented + answered. Snapshot-then-iterate (gate-1 F-5);
// bounded re-drain loop (cap at maxDrainRounds).
//
// On ctx-cancel returns ctx.Err(); pending items stay queued for
// the next turn (gate-1 F-14).
func (d *modalDriver) DrainPending(ctx context.Context) error {
	for round := 0; round < maxDrainRounds; round++ {
		d.mu.Lock()
		snapshot := d.pending
		d.pending = nil
		d.mu.Unlock()
		if len(snapshot) == 0 {
			return nil
		}
		for _, req := range snapshot {
			if err := d.handleRequest(ctx, req); err != nil {
				// Re-queue the request so the next turn sees it; but
				// only if it was a ctx-cancel (transient). Other errors
				// drop the request to avoid an infinite loop.
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					d.mu.Lock()
					d.pending = append([]modalRequest{req}, d.pending...)
					d.mu.Unlock()
				} else if req.attestRef != "" {
					d.mu.Lock()
					delete(d.inFlight, req.attestRef)
					d.mu.Unlock()
				}
				return err
			}
			if req.attestRef != "" {
				d.mu.Lock()
				delete(d.inFlight, req.attestRef)
				d.mu.Unlock()
			}
		}
	}
	if d.bus != nil {
		d.bus.Publish(runner.OperatorEvent{
			Kind:   runner.OpEventModalBackpressure,
			Detail: fmt.Sprintf("DrainPending exceeded %d rounds; runaway publisher?", maxDrainRounds),
		})
	}
	return ErrModalDrainCapExceeded
}

// handleRequest dispatches a single modal request. For verdicts
// it calls PresentVerdict; for escalations PresentEscalation.
// Both paths construct an AttestationRecord and persist via
// store.Record.
func (d *modalDriver) handleRequest(ctx context.Context, req modalRequest) error {
	switch req.kind {
	case "verdict":
		// If the clause is already crossed (gate-1 F-7), present
		// the escalation prompt instead of a vanilla verdict.
		if d.ibTracker != nil && d.ibTracker.IsCrossed(req.clauseID) {
			return d.handleEscalation(ctx, req)
		}
		return d.handleVerdict(ctx, req)
	case "escalation":
		return d.handleEscalation(ctx, req)
	default:
		return fmt.Errorf("modal-driver: unknown request kind %q", req.kind)
	}
}

func (d *modalDriver) handleVerdict(ctx context.Context, req modalRequest) error {
	sub, err := d.prompt.PresentVerdict(ctx, req.hint)
	if err != nil {
		if errors.Is(err, modal.ErrModalSkipped) {
			if d.bus != nil {
				d.bus.Publish(runner.OperatorEvent{
					Kind:     runner.OpEventModalSkipped,
					ArrowID:  req.arrowID,
					PassID:   req.passID,
					ClauseID: req.clauseID,
					Detail:   "operator skipped; clause stays pending",
				})
			}
			return nil
		}
		return err
	}
	rec, recErr := d.buildRecord(req, sub)
	if recErr != nil {
		return recErr
	}
	if err := d.store.Record(rec); err != nil {
		return fmt.Errorf("modal-driver: Record: %w", err)
	}
	// Update IB tracker per verdict (matches the existing
	// session_engine wiring's Observe-based path; the modal
	// driver calls Record which fires the observer too — so this
	// is the audit-trail signal).
	if d.bus != nil && sub.Verdict == runner.AttestationFail {
		d.bus.Publish(runner.OperatorEvent{
			Kind:     runner.OpEventClauseFailVerdict,
			ArrowID:  req.arrowID,
			PassID:   req.passID,
			ClauseID: req.clauseID,
			Detail:   "operator returned fail",
		})
	}
	return nil
}

func (d *modalDriver) handleEscalation(ctx context.Context, req modalRequest) error {
	if d.bus != nil {
		d.bus.Publish(runner.OperatorEvent{
			Kind:     runner.OpEventEscalationPresented,
			ArrowID:  req.arrowID,
			PassID:   req.passID,
			ClauseID: req.clauseID,
		})
	}
	choice, err := d.prompt.PresentEscalation(ctx, req.hint)
	if err != nil {
		return err
	}
	// Option 1 (accept-risk + residue) → AttestationPass with the
	// residue note: operator decides the basis is sufficient
	// given the residue context.
	// Option 2 (route-upstream + rationale) → AttestationFail
	// with the rationale: pass aborts; deeper retry happens later.
	// Both verdicts go through validateAttestation, which only
	// accepts pass/fail/insufficient-basis.
	var verdict runner.AttestationVerdict
	if choice.Option == 1 {
		verdict = runner.AttestationPass
	} else {
		verdict = runner.AttestationFail
	}
	sub := modal.VerdictSubmission{
		Verdict: verdict,
		Unit:    runner.VerdictUnitWriteResidueNote,
		Payload: runner.VerdictUnitPayload{Residue: choice.Residue},
	}
	rec, recErr := d.buildRecord(req, sub)
	if recErr != nil {
		return recErr
	}
	if err := d.store.Record(rec); err != nil {
		return fmt.Errorf("modal-driver: escalation Record: %w", err)
	}
	// Gate-1 F-7: Reset clears the sticky flag so a future
	// insufficient-basis count starts fresh.
	if d.ibTracker != nil {
		d.ibTracker.Reset(req.clauseID)
	}
	if d.bus != nil {
		d.bus.Publish(runner.OperatorEvent{
			Kind:     runner.OpEventEscalationResolved,
			ArrowID:  req.arrowID,
			PassID:   req.passID,
			ClauseID: req.clauseID,
			Detail:   fmt.Sprintf("option=%d", choice.Option),
		})
	}
	return nil
}

// buildRecord constructs an AttestationRecord from a modal
// submission. Reads the op-id via opIDProvider AT CALL TIME
// (gate-1 F-20).
func (d *modalDriver) buildRecord(req modalRequest, sub modal.VerdictSubmission) (runner.AttestationRecord, error) {
	opID := ""
	if d.opIDProvider != nil {
		opID = d.opIDProvider()
	}
	if opID == "" {
		return runner.AttestationRecord{}, errors.New("modal-driver: no active op-id (use /op-id first)")
	}
	// Resolve per-arrow metadata (SourceRole, TargetRole, Context,
	// Stratum, GridVersion). Mirrors the /attest CLI's
	// grid.Lookup-based fill (cmd/ghyll/session.go).
	src, tgt, ctxStr, stratum, gridVer := req.sourceRole, req.targetRole, req.context, req.stratum, req.gridVersion
	if d.arrowResolver != nil {
		if resolved, ok := d.arrowResolver(req.arrowID); ok {
			if src == "" {
				src = resolved.SourceRole
			}
			if tgt == "" {
				tgt = resolved.TargetRole
			}
			if ctxStr == "" {
				ctxStr = resolved.Context
			}
			if stratum == "" {
				stratum = resolved.Stratum
			}
			if gridVer == 0 {
				gridVer = resolved.GridVersion
			}
		}
	}
	payloadJSON, err := json.Marshal(sub.Payload)
	if err != nil {
		return runner.AttestationRecord{}, fmt.Errorf("modal-driver: payload marshal: %w", err)
	}
	hintJSON, _ := json.Marshal(req.hint)
	return runner.AttestationRecord{
		ID:              req.attestRef,
		Kind:            runner.AttestationKindDepthType,
		ArrowID:         req.arrowID,
		ClauseID:        req.clauseID,
		OpID:            opID,
		AttestedByRole:  "operator",
		SourceRole:      src,
		TargetRole:      tgt,
		Verdict:         sub.Verdict,
		Timestamp:       time.Now().UnixNano(),
		GridVersion:     gridVer,
		PassID:          req.passID,
		Context:         ctxStr,
		Stratum:         stratum,
		Unit:            sub.Unit,
		UnitPayload:     sub.Payload,
		UnitPayloadJSON: string(payloadJSON),
		HintJSON:        string(hintJSON),
	}, nil
}

// PendingLen returns the current queue length. Exposed for tests
// + future status CLI integration.
func (d *modalDriver) PendingLen() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pending)
}
