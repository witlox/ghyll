package main

import (
	gocontext "context"
	"errors"
	"testing"
	"time"
)

// TestScenario_Session_Exit_CancelsSessionContext verifies that
// /exit fires sessionCancel so any in-flight modal read aborts
// promptly with ctx.Err() (gate-1 F-14).
func TestScenario_Session_Exit_CancelsSessionContext(t *testing.T) {
	ctx, cancel := gocontext.WithCancel(gocontext.Background())
	s := &Session{
		sessionCtx:    ctx,
		sessionCancel: cancel,
		output:        func(string) {},
	}
	r := s.DispatchSlashCommand("/exit")
	if !r.Handled || !r.ExitRequested {
		t.Fatalf("/exit result = %+v; want Handled+ExitRequested", r)
	}
	select {
	case <-s.SessionContext().Done():
		// good
	case <-time.After(time.Second):
		t.Fatal("sessionCtx not cancelled after /exit")
	}
	if !errors.Is(s.SessionContext().Err(), gocontext.Canceled) {
		t.Errorf("sessionCtx.Err() = %v; want context.Canceled", s.SessionContext().Err())
	}
}

// TestScenario_Session_Close_IdempotentCancel verifies multiple
// Close calls (e.g. via /exit + signal-handler defer) do not
// panic.
func TestScenario_Session_Close_IdempotentCancel(t *testing.T) {
	ctx, cancel := gocontext.WithCancel(gocontext.Background())
	s := &Session{
		sessionCtx:    ctx,
		sessionCancel: cancel,
		output:        func(string) {},
	}
	s.Close()
	s.Close() // second call must be a no-op
}

// TestScenario_Session_SessionContext_FallbackBackground verifies
// SessionContext returns Background when the session was built
// directly without NewSession (test fixtures).
func TestScenario_Session_SessionContext_FallbackBackground(t *testing.T) {
	s := &Session{output: func(string) {}}
	if c := s.SessionContext(); c == nil {
		t.Fatal("SessionContext returned nil")
	}
	if err := s.SessionContext().Err(); err != nil {
		t.Errorf("background ctx Err = %v; want nil", err)
	}
}
