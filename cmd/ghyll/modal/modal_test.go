package modal

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/witlox/ghyll/runner"
)

func TestScenario_TermModal_PassVerdict(t *testing.T) {
	in := strings.NewReader("pass\n")
	out := &bytes.Buffer{}
	m := &TermModal{In: in, Out: out}
	v, err := m.PresentVerdict(context.Background(), Hint{ArrowID: "A1", ClauseID: "C1"})
	if err != nil {
		t.Fatalf("PresentVerdict: %v", err)
	}
	if v.Verdict != runner.AttestationPass {
		t.Errorf("verdict = %q; want pass", v.Verdict)
	}
	if v.Unit != runner.VerdictUnitConfirm {
		t.Errorf("unit = %q; want confirm", v.Unit)
	}
	if !strings.Contains(out.String(), "verdict?") {
		t.Errorf("prompt missing from output:\n%s", out.String())
	}
}

func TestScenario_TermModal_FailVerdict(t *testing.T) {
	in := strings.NewReader("fail\nfile.go:42-50, other.go:1\n")
	out := &bytes.Buffer{}
	m := &TermModal{In: in, Out: out}
	v, err := m.PresentVerdict(context.Background(), Hint{ArrowID: "A1"})
	if err != nil {
		t.Fatalf("PresentVerdict: %v", err)
	}
	if v.Verdict != runner.AttestationFail {
		t.Errorf("verdict = %q; want fail", v.Verdict)
	}
	if v.Unit != runner.VerdictUnitRecordLocationsInspected {
		t.Errorf("unit = %q", v.Unit)
	}
	if len(v.Payload.Inspected) != 2 {
		t.Errorf("inspected = %v; want 2 entries", v.Payload.Inspected)
	}
}

func TestScenario_TermModal_InsufficientBasisVerdict(t *testing.T) {
	in := strings.NewReader("insufficient-basis\nfeature too large\n")
	out := &bytes.Buffer{}
	m := &TermModal{In: in, Out: out}
	v, err := m.PresentVerdict(context.Background(), Hint{ArrowID: "A1"})
	if err != nil {
		t.Fatalf("PresentVerdict: %v", err)
	}
	if v.Unit != runner.VerdictUnitWriteResidueNote {
		t.Errorf("unit = %q", v.Unit)
	}
	if v.Payload.Residue != "feature too large" {
		t.Errorf("residue = %q", v.Payload.Residue)
	}
}

func TestScenario_TermModal_SkipVerdict(t *testing.T) {
	in := strings.NewReader("skip\n")
	out := &bytes.Buffer{}
	m := &TermModal{In: in, Out: out}
	_, err := m.PresentVerdict(context.Background(), Hint{ArrowID: "A1"})
	if !errors.Is(err, ErrModalSkipped) {
		t.Errorf("err = %v; want ErrModalSkipped", err)
	}
}

func TestScenario_TermModal_InvalidVerdictRejected(t *testing.T) {
	in := strings.NewReader("yeah-sure\n")
	out := &bytes.Buffer{}
	m := &TermModal{In: in, Out: out}
	_, err := m.PresentVerdict(context.Background(), Hint{ArrowID: "A1"})
	if err == nil || strings.Contains(err.Error(), "ErrModalSkipped") {
		t.Errorf("unrecognized verdict should error explicitly; got %v", err)
	}
}

func TestScenario_TermModal_ContextCancelAbortsRead(t *testing.T) {
	// Use a pipe that never sends — context cancel aborts the read.
	r, w, err := pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	out := &bytes.Buffer{}
	m := &TermModal{In: r, Out: out}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	_, err = m.PresentVerdict(ctx, Hint{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want context.Canceled", err)
	}
}

func TestScenario_TermModal_EscalationOption1(t *testing.T) {
	in := strings.NewReader("1\nresidue text\n")
	out := &bytes.Buffer{}
	m := &TermModal{In: in, Out: out}
	c, err := m.PresentEscalation(context.Background(), Hint{ArrowID: "A1"})
	if err != nil {
		t.Fatalf("PresentEscalation: %v", err)
	}
	if c.Option != 1 || c.Residue != "residue text" {
		t.Errorf("choice = %+v", c)
	}
}

func TestScenario_TermModal_EscalationOption2(t *testing.T) {
	in := strings.NewReader("2\nrationale text\n")
	out := &bytes.Buffer{}
	m := &TermModal{In: in, Out: out}
	c, err := m.PresentEscalation(context.Background(), Hint{ArrowID: "A1"})
	if err != nil {
		t.Fatalf("PresentEscalation: %v", err)
	}
	if c.Option != 2 || c.Residue != "rationale text" {
		t.Errorf("choice = %+v", c)
	}
}

func TestScenario_TermModal_EscalationRejectsInvalidChoice(t *testing.T) {
	in := strings.NewReader("3\n")
	out := &bytes.Buffer{}
	m := &TermModal{In: in, Out: out}
	_, err := m.PresentEscalation(context.Background(), Hint{ArrowID: "A1"})
	if !errors.Is(err, ErrEscalationNoDefault) {
		t.Errorf("err = %v; want ErrEscalationNoDefault", err)
	}
}

func TestScenario_StubModal_VerdictQueue(t *testing.T) {
	m := &StubModal{
		Verdicts: []VerdictSubmission{
			{Verdict: runner.AttestationPass, Unit: runner.VerdictUnitConfirm},
			{Verdict: runner.AttestationFail, Unit: runner.VerdictUnitRecordLocationsInspected, Payload: runner.VerdictUnitPayload{Inspected: []string{"f:1"}}},
		},
	}
	v1, _ := m.PresentVerdict(context.Background(), Hint{})
	if v1.Verdict != runner.AttestationPass {
		t.Errorf("v1 = %q", v1.Verdict)
	}
	v2, _ := m.PresentVerdict(context.Background(), Hint{})
	if v2.Verdict != runner.AttestationFail {
		t.Errorf("v2 = %q", v2.Verdict)
	}
	// Queue exhausted → ErrModalSkipped.
	_, err := m.PresentVerdict(context.Background(), Hint{})
	if !errors.Is(err, ErrModalSkipped) {
		t.Errorf("exhausted queue: %v", err)
	}
}

func TestScenario_StubModal_VerdictErrInjection(t *testing.T) {
	customErr := errors.New("custom")
	m := &StubModal{
		Verdicts:    []VerdictSubmission{{}},
		VerdictErrs: []error{customErr},
	}
	_, err := m.PresentVerdict(context.Background(), Hint{})
	if !errors.Is(err, customErr) {
		t.Errorf("err = %v; want customErr", err)
	}
}

func TestScenario_StubModal_EscalationQueue(t *testing.T) {
	m := &StubModal{
		Escalations: []EscalationChoice{
			{Option: 1, Residue: "accepted"},
		},
	}
	c, err := m.PresentEscalation(context.Background(), Hint{})
	if err != nil {
		t.Fatalf("PresentEscalation: %v", err)
	}
	if c.Option != 1 || c.Residue != "accepted" {
		t.Errorf("c = %+v", c)
	}
	// Queue exhausted → ErrEscalationNoDefault.
	_, err = m.PresentEscalation(context.Background(), Hint{})
	if !errors.Is(err, ErrEscalationNoDefault) {
		t.Errorf("exhausted: %v", err)
	}
}
